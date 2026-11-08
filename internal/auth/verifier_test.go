package auth_test

import (
	"context"
	"crypto/x509"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/auth/authtest"
)

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newVerifier(realm *authtest.Realm, c *clock, discover bool) *auth.Verifier {
	o := auth.Options{
		Issuer:   realm.Issuer(),
		Audience: authtest.Audience,
		Now:      c.now,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if !discover {
		o.JWKSURL = realm.JWKSURL()
	}
	return auth.NewVerifier(o)
}

func TestVerifierAuthenticate(t *testing.T) {
	key := authtest.NewKey(t, "k1")
	other := authtest.NewKey(t, "k1") // same kid, different key: bad signature
	realm := authtest.NewRealm(t, key)

	with := func(edit func(jwt.MapClaims)) jwt.MapClaims {
		c := realm.Claims(now, "editor")
		edit(c)
		return c
	}
	bearer := func(claims jwt.MapClaims) string { return "Bearer " + authtest.Sign(t, key, claims) }

	none := jwt.NewWithClaims(jwt.SigningMethodNone, realm.Claims(now, "editor"))
	none.Header["kid"] = "k1"
	noneToken, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	pubDER, err := x509.MarshalPKIXPublicKey(&key.Private.PublicKey)
	require.NoError(t, err)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, realm.Claims(now, "editor"))
	hs.Header["kid"] = "k1"
	hsToken, err := hs.SignedString(pubDER)
	require.NoError(t, err)

	tests := []struct {
		name          string
		authorization string
		wantErr       bool
	}{
		{"valid", bearer(realm.Claims(now, "editor")), false},
		{"valid with lowercase scheme", "bearer " + authtest.Sign(t, key, realm.Claims(now, "editor")), false},
		{"valid with string audience", bearer(with(func(c jwt.MapClaims) { c["aud"] = authtest.Audience })), false},
		{"no token", "", true},
		{"empty bearer", "Bearer ", true},
		{"non-Bearer scheme", "Basic dXNlcjpwYXNz", true},
		{"malformed", "Bearer not.a.jwt", true},
		{"alg none", "Bearer " + noneToken, true},
		{"alg HS256", "Bearer " + hsToken, true},
		{"bad signature", "Bearer " + authtest.Sign(t, other, realm.Claims(now, "editor")), true},
		{"wrong issuer", bearer(with(func(c jwt.MapClaims) { c["iss"] = "http://evil.example/realms/arch" })), true},
		{"wrong audience string", bearer(with(func(c jwt.MapClaims) { c["aud"] = "account" })), true},
		{"wrong audience array", bearer(with(func(c jwt.MapClaims) { c["aud"] = []string{"account", "arch-web"} })), true},
		{"no exp", bearer(with(func(c jwt.MapClaims) { delete(c, "exp") })), true},
		{"expired 31 s rejected", bearer(with(func(c jwt.MapClaims) { c["exp"] = now.Add(-31 * time.Second).Unix() })), true},
		{"expired 29 s accepted", bearer(with(func(c jwt.MapClaims) { c["exp"] = now.Add(-29 * time.Second).Unix() })), false},
		{"nbf 31 s ahead rejected", bearer(with(func(c jwt.MapClaims) { c["nbf"] = now.Add(31 * time.Second).Unix() })), true},
		{"nbf 29 s ahead accepted", bearer(with(func(c jwt.MapClaims) { c["nbf"] = now.Add(29 * time.Second).Unix() })), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newVerifier(realm, &clock{now}, false)
			p, err := v.Authenticate(context.Background(), tt.authorization)
			if tt.wantErr {
				require.ErrorIs(t, err, auth.ErrUnauthenticated)
				assert.Equal(t, auth.Principal{}, p)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "8d0c6d4e-user", p.Subject)
		})
	}
}

func TestVerifierDiscoversJWKSOnFirstToken(t *testing.T) {
	key := authtest.NewKey(t, "k1")
	realm := authtest.NewRealm(t, key)
	v := newVerifier(realm, &clock{now}, true)
	assert.Equal(t, 0, realm.Fetches(), "no fetch before the first token")

	_, err := v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, key, realm.Claims(now, "viewer")))
	require.NoError(t, err)
	assert.Equal(t, 1, realm.Fetches())
}

func TestVerifierJWKSFetchErrorIsUnauthenticated(t *testing.T) {
	key := authtest.NewKey(t, "k1")
	realm := authtest.NewRealm(t, key)
	v := auth.NewVerifier(auth.Options{
		Issuer:   realm.Issuer(),
		Audience: authtest.Audience,
		JWKSURL:  realm.Issuer() + "/missing",
		Now:      (&clock{now}).now,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	_, err := v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, key, realm.Claims(now, "viewer")))
	require.ErrorIs(t, err, auth.ErrUnauthenticated)
}

func TestVerifierUnknownKid(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	k2 := authtest.NewKey(t, "k2")
	k3 := authtest.NewKey(t, "k3")

	t.Run("unknown kid one refetch then accepted", func(t *testing.T) {
		realm := authtest.NewRealm(t, k1)
		c := &clock{now}
		v := newVerifier(realm, c, false)

		_, err := v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k1, realm.Claims(now, "viewer")))
		require.NoError(t, err)
		require.Equal(t, 1, realm.Fetches())

		realm.SetKeys(k1, k2) // key rotation
		_, err = v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k2, realm.Claims(now, "viewer")))
		require.NoError(t, err)
		assert.Equal(t, 2, realm.Fetches(), "exactly one refetch")

		_, err = v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k2, realm.Claims(now, "viewer")))
		require.NoError(t, err)
		assert.Equal(t, 2, realm.Fetches(), "the new key is cached")
	})

	t.Run("second unknown kid within 30 s no refetch", func(t *testing.T) {
		realm := authtest.NewRealm(t, k1)
		c := &clock{now}
		v := newVerifier(realm, c, false)

		_, err := v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k1, realm.Claims(now, "viewer")))
		require.NoError(t, err)

		_, err = v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k2, realm.Claims(now, "viewer")))
		require.ErrorIs(t, err, auth.ErrUnauthenticated, "k2 is not in the JWKS")
		require.Equal(t, 2, realm.Fetches())

		realm.SetKeys(k1, k3)
		c.t = now.Add(29 * time.Second)
		_, err = v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k3, realm.Claims(c.t, "viewer")))
		require.ErrorIs(t, err, auth.ErrUnauthenticated)
		assert.Equal(t, 2, realm.Fetches(), "rate-limited: no extra refetch")

		c.t = now.Add(auth.RefetchInterval)
		_, err = v.Authenticate(context.Background(), "Bearer "+authtest.Sign(t, k3, realm.Claims(c.t, "viewer")))
		require.NoError(t, err)
		assert.Equal(t, 3, realm.Fetches(), "refetch allowed again after 30 s")
	})
}
