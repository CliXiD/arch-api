package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RefetchInterval is the minimum time between two JWKS refetches caused by an unknown `kid`.
const RefetchInterval = 30 * time.Second

const maxJWKSBytes = 1 << 20

var errUnknownKey = errors.New("unknown signing key")

// publicKey is one verification key from the JWKS.
type publicKey struct {
	alg   string // optional `alg` of the JWK
	kty   string // RSA or EC
	curve string // EC only
	key   any    // *rsa.PublicKey or *ecdsa.PublicKey
}

// keySet caches the realm's signing keys in memory. Discovery and the first fetch are lazy.
type keySet struct {
	issuer  string
	jwksURL string // configured, or discovered on first use
	client  *http.Client
	now     func() time.Time
	logger  *slog.Logger

	mu          sync.Mutex
	keys        map[string]publicKey
	loaded      bool
	lastRefetch time.Time
}

// get returns the key for kid. An unknown kid triggers one refetch, at most once per
// RefetchInterval.
func (s *keySet) get(ctx context.Context, kid string) (publicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.loaded {
		if err := s.fetch(ctx); err != nil {
			return publicKey{}, err
		}
		s.loaded = true
		if k, ok := s.keys[kid]; ok {
			return k, nil
		}
		return publicKey{}, errUnknownKey
	}
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}

	now := s.now()
	if !s.lastRefetch.IsZero() && now.Sub(s.lastRefetch) < RefetchInterval {
		return publicKey{}, errUnknownKey
	}
	s.lastRefetch = now
	if err := s.fetch(ctx); err != nil {
		return publicKey{}, err
	}
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}
	return publicKey{}, errUnknownKey
}

func (s *keySet) fetch(ctx context.Context) error {
	if s.jwksURL == "" {
		var doc struct {
			JWKSURI string `json:"jwks_uri"`
		}
		if err := s.getJSON(ctx, strings.TrimRight(s.issuer, "/")+"/.well-known/openid-configuration", &doc); err != nil {
			s.logger.ErrorContext(ctx, "oidc discovery failed", "error", err.Error())
			return err
		}
		if doc.JWKSURI == "" {
			s.logger.ErrorContext(ctx, "oidc discovery failed", "error", "no jwks_uri")
			return errors.New("discovery: no jwks_uri")
		}
		s.jwksURL = doc.JWKSURI
	}

	var set struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := s.getJSON(ctx, s.jwksURL, &set); err != nil {
		s.logger.ErrorContext(ctx, "jwks fetch failed", "error", err.Error())
		return err
	}
	keys := make(map[string]publicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pk, err := k.publicKey()
		if err != nil {
			s.logger.WarnContext(ctx, "jwks key skipped", "kid", k.Kid, "error", err.Error())
			continue
		}
		keys[k.Kid] = pk
	}
	s.keys = keys
	return nil
}

func (s *keySet) getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxJWKSBytes)).Decode(v)
}

type jsonWebKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jsonWebKey) publicKey() (publicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return publicKey{}, fmt.Errorf("bad n: %w", err)
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return publicKey{}, fmt.Errorf("bad e: %w", err)
		}
		exp := new(big.Int).SetBytes(e)
		if len(n) == 0 || !exp.IsInt64() || exp.Int64() < 3 || exp.Int64() > 1<<31-1 {
			return publicKey{}, errors.New("bad RSA key")
		}
		return publicKey{alg: k.Alg, kty: k.Kty, key: &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp.Int64())}}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		default:
			return publicKey{}, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return publicKey{}, fmt.Errorf("bad x: %w", err)
		}
		y, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return publicKey{}, fmt.Errorf("bad y: %w", err)
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(x) > size || len(y) > size {
			return publicKey{}, errors.New("bad EC key")
		}
		point := make([]byte, 1+2*size)
		point[0] = 4
		copy(point[1+size-len(x):1+size], x)
		copy(point[1+2*size-len(y):], y)
		pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
		if err != nil {
			return publicKey{}, fmt.Errorf("bad EC key: %w", err)
		}
		return publicKey{alg: k.Alg, kty: k.Kty, curve: k.Crv, key: pub}, nil
	default:
		return publicKey{}, fmt.Errorf("unsupported kty %q", k.Kty)
	}
}
