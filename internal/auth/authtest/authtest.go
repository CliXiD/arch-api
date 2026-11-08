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
// Both endpoints can be made to fail (SetDown) or to wait until released (Block).
type Realm struct {
	Server  *httptest.Server
	mu      sync.Mutex
	keys    []Key
	down    bool
	gate    chan struct{}
	arrived chan struct{}
	fetches atomic.Int32
}

// NewRealm starts a fake realm serving keys. It is closed when the test ends.
func NewRealm(t testing.TB, keys ...Key) *Realm {
	t.Helper()
	r := &Realm{keys: keys, arrived: make(chan struct{}, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, req *http.Request) {
		if !r.pass(w, req) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": r.Issuer(), "jwks_uri": r.JWKSURL()})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, req *http.Request) {
		r.fetches.Add(1)
		if !r.pass(w, req) {
			return
		}
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

// pass waits while the realm is blocked, then answers 503 and returns false when it is down.
func (r *Realm) pass(w http.ResponseWriter, req *http.Request) bool {
	r.mu.Lock()
	gate := r.gate
	r.mu.Unlock()
	if gate != nil {
		select {
		case r.arrived <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-req.Context().Done():
			return false
		}
	}
	r.mu.Lock()
	down := r.down
	r.mu.Unlock()
	if down {
		w.WriteHeader(http.StatusServiceUnavailable)
		return false
	}
	return true
}

// SetDown makes discovery and the JWKS endpoint answer 503 (true) or serve again (false).
func (r *Realm) SetDown(down bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.down = down
}

// Block makes discovery and JWKS requests wait until release is called. A JWKS request is counted
// in Fetches when it starts, before it waits; Arrived receives once per waiting request.
func (r *Realm) Block() (release func()) {
	gate := make(chan struct{})
	r.mu.Lock()
	r.gate = gate
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.gate == gate {
				r.gate = nil
			}
			r.mu.Unlock()
			close(gate)
		})
	}
}

// Arrived receives once for every request that reached a blocked endpoint.
func (r *Realm) Arrived() <-chan struct{} { return r.arrived }

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
