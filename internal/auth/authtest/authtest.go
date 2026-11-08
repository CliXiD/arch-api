// Package authtest serves a fake realm JWKS and signs test tokens. For tests only.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audience is the audience test tokens carry by default.
const Audience = "arch-api"

// Key is an RSA signing key with its `kid`.
type Key struct {
	Kid     string
	Private *rsa.PrivateKey
}

// NewKey generates a 2048-bit RSA key.
func NewKey(t testing.TB, kid string) Key {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return Key{Kid: kid, Private: k}
}

// Realm is an httptest server with OIDC discovery and a JWKS endpoint that counts its fetches.
type Realm struct {
	Server  *httptest.Server
	mu      sync.Mutex
	keys    []Key
	fetches atomic.Int32
}

// NewRealm starts a fake realm serving keys. It is closed when the test ends.
func NewRealm(t testing.TB, keys ...Key) *Realm {
	t.Helper()
	r := &Realm{keys: keys}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": r.Issuer(), "jwks_uri": r.JWKSURL()})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		r.fetches.Add(1)
		r.mu.Lock()
		defer r.mu.Unlock()
		set := make([]map[string]string, 0, len(r.keys))
		for _, k := range r.keys {
			pub := k.Private.PublicKey
			set = append(set, map[string]string{
				"kid": k.Kid, "kty": "RSA", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": set})
	})
	r.Server = httptest.NewServer(mux)
	t.Cleanup(r.Server.Close)
	return r
}

// Issuer is the realm's issuer URL.
func (r *Realm) Issuer() string { return r.Server.URL }

// JWKSURL is the realm's JWKS URL.
func (r *Realm) JWKSURL() string { return r.Server.URL + "/jwks" }

// SetKeys replaces the served keys, as a key rotation does.
func (r *Realm) SetKeys(keys ...Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = keys
}

// Fetches is the number of JWKS requests served so far.
func (r *Realm) Fetches() int { return int(r.fetches.Load()) }

// Claims returns valid claims for a user of this realm at now.
func (r *Realm) Claims(now time.Time, realmRoles ...string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":                r.Issuer(),
		"aud":                []string{Audience, "account"},
		"sub":                "8d0c6d4e-user",
		"exp":                now.Add(5 * time.Minute).Unix(),
		"iat":                now.Unix(),
		"name":               "Editor Local",
		"preferred_username": "editor",
		"email":              "editor@example.test",
		"realm_access":       map[string]any{"roles": append([]string{"default-roles-arch", "offline_access"}, realmRoles...)},
	}
}

// Sign returns a compact RS256 JWT with key's kid.
func Sign(t testing.TB, key Key, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = key.Kid
	s, err := tok.SignedString(key.Private)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
