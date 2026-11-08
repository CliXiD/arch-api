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

	"golang.org/x/sync/singleflight"
)

// RefetchInterval is the minimum time between two JWKS refetches caused by an unknown `kid`.
const RefetchInterval = 30 * time.Second

// FailureBackoff is the minimum time between a failed discovery or JWKS fetch and the next one.
const FailureBackoff = 5 * time.Second

const maxJWKSBytes = 1 << 20

var (
	errUnknownKey = errors.New("unknown signing key")
	// errKeysUnavailable means the keys have never been loaded and none could be obtained.
	errKeysUnavailable = errors.New("signing keys are not available")
)

// publicKey is one verification key from the JWKS.
type publicKey struct {
	alg   string // optional `alg` of the JWK
	kty   string // RSA or EC
	curve string // EC only
	key   any    // *rsa.PublicKey or *ecdsa.PublicKey
}

// keySet caches the realm's signing keys in memory. Discovery and the first fetch are lazy.
// mu guards only the in-memory state and is never held during discovery or an HTTP call; every
// fetch runs through group, so concurrent callers share one.
type keySet struct {
	issuer string
	client *http.Client
	now    func() time.Time
	logger *slog.Logger
	group  singleflight.Group

	mu          sync.Mutex
	jwksURL     string // configured, or discovered on first use
	keys        map[string]publicKey
	loaded      bool
	lastAttempt time.Time // start of the last fetch
	failed      bool      // the last fetch failed
	lastRefetch time.Time // start of the last unknown-kid refetch after the keys were loaded
}

// get returns the key for kid. A kid that is not cached triggers a fetch, shared with every
// concurrent caller. After a failed fetch no new one starts for FailureBackoff; once the keys are
// loaded, unknown-kid refetches start at most once per RefetchInterval. It returns
// errKeysUnavailable while the keys have never been loaded, errUnknownKey otherwise.
func (s *keySet) get(ctx context.Context, kid string) (publicKey, error) {
	if k, ok := s.lookup(kid); ok {
		return k, nil
	}
	_, _, _ = s.group.Do("jwks", func() (any, error) {
		s.refresh(context.WithoutCancel(ctx))
		return nil, nil
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}
	if !s.loaded {
		return publicKey{}, errKeysUnavailable
	}
	return publicKey{}, errUnknownKey
}

func (s *keySet) lookup(kid string) (publicKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[kid]
	return k, ok
}

// refresh fetches the keys when the backoff allows it. A failed fetch leaves the loaded keys as
// they are; a successful one replaces them.
func (s *keySet) refresh(ctx context.Context) {
	s.mu.Lock()
	now := s.now()
	if !s.mayFetch(now) {
		s.mu.Unlock()
		return
	}
	s.lastAttempt = now
	if s.loaded {
		s.lastRefetch = now
	}
	jwksURL := s.jwksURL
	s.mu.Unlock()

	keys, jwksURL, err := s.fetch(ctx, jwksURL)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.jwksURL = jwksURL
	s.failed = err != nil
	if err == nil {
		s.keys = keys
		s.loaded = true
	}
}

// mayFetch reports whether a fetch may start at now. After a failed fetch only FailureBackoff
// applies, so a failed refetch does not hold back key rotation for a whole RefetchInterval.
// Callers hold mu.
func (s *keySet) mayFetch(now time.Time) bool {
	if s.failed {
		return now.Sub(s.lastAttempt) >= FailureBackoff
	}
	if s.loaded {
		return s.lastRefetch.IsZero() || now.Sub(s.lastRefetch) >= RefetchInterval
	}
	return true
}

// fetch discovers the JWKS URL when jwksURL is empty, then fetches the keys. It returns the JWKS
// URL it used (or discovered), also on a failed JWKS fetch.
func (s *keySet) fetch(ctx context.Context, jwksURL string) (map[string]publicKey, string, error) {
	if jwksURL == "" {
		var doc struct {
			JWKSURI string `json:"jwks_uri"`
		}
		if err := s.getJSON(ctx, strings.TrimRight(s.issuer, "/")+"/.well-known/openid-configuration", &doc); err != nil {
			s.logger.ErrorContext(ctx, "oidc discovery failed", "error", err.Error())
			return nil, "", err
		}
		if doc.JWKSURI == "" {
			s.logger.ErrorContext(ctx, "oidc discovery failed", "error", "no jwks_uri")
			return nil, "", errors.New("discovery: no jwks_uri")
		}
		jwksURL = doc.JWKSURI
	}

	var set struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := s.getJSON(ctx, jwksURL, &set); err != nil {
		s.logger.ErrorContext(ctx, "jwks fetch failed", "error", err.Error())
		return nil, jwksURL, err
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
	return keys, jwksURL, nil
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
