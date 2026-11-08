package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Leeway is the clock skew allowed on `exp` and `nbf`.
const Leeway = 30 * time.Second

// ErrUnauthenticated means the request has no valid bearer token. It never carries token or
// claim values.
var ErrUnauthenticated = errors.New("a valid bearer token is required")

// ErrUnavailable means a token could not be checked because the realm's signing keys have never
// been loaded (the fetch failed, or is held back by FailureBackoff). It never carries token or
// claim values.
var ErrUnavailable = errors.New("signing keys are not available")

// allowedAlgs are the asymmetric algorithms accepted; `none` and HMAC are always rejected.
var allowedAlgs = []string{"RS256", "RS384", "RS512", "PS256", "ES256", "ES384"}

// Options configure a Verifier.
type Options struct {
	Issuer   string
	Audience string
	// JWKSURL is optional; when empty it is discovered from the issuer on the first token.
	JWKSURL    string
	HTTPClient *http.Client
	// Now is the clock; time.Now when nil.
	Now    func() time.Time
	Logger *slog.Logger
}

// Verifier validates bearer tokens against the realm's JWKS.
type Verifier struct {
	issuer   string
	audience string
	now      func() time.Time
	keys     *keySet
}

// NewVerifier returns a Verifier. It makes no network call until the first token.
func NewVerifier(o Options) *Verifier {
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Verifier{
		issuer:   o.Issuer,
		audience: o.Audience,
		now:      o.Now,
		keys: &keySet{
			issuer:  o.Issuer,
			jwksURL: o.JWKSURL,
			client:  o.HTTPClient,
			now:     o.Now,
			logger:  o.Logger,
		},
	}
}

// Authenticate validates the `Authorization` header value and returns the caller. A missing,
// empty or non-Bearer header is ErrUnauthenticated, before any key lookup; other failures are
// those of Verify.
func (v *Verifier) Authenticate(ctx context.Context, authorization string) (Principal, error) {
	scheme, token, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return Principal{}, ErrUnauthenticated
	}
	claims, err := v.Verify(ctx, strings.TrimSpace(token))
	if err != nil {
		return Principal{}, err
	}
	return PrincipalFromClaims(claims), nil
}

// tokenClaims is what a token is decoded into: the registered claims plus Claims.
type tokenClaims struct {
	jwt.RegisteredClaims
	Claims
}

// Verify checks the token's signature, issuer, audience and validity window, and returns its
// claims. It returns ErrUnavailable when the signing keys have never been loaded, and
// ErrUnauthenticated for every other failure.
func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods(allowedAlgs),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(Leeway),
		jwt.WithTimeFunc(v.now),
	)
	var tc tokenClaims
	_, err := parser.ParseWithClaims(raw, &tc, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errUnknownKey
		}
		k, err := v.keys.get(ctx, kid)
		if err != nil {
			return nil, err
		}
		if !keyMatches(k, t.Method.Alg()) {
			return nil, errors.New("algorithm does not match key")
		}
		return k.key, nil
	})
	if errors.Is(err, errKeysUnavailable) {
		return Claims{}, ErrUnavailable
	}
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	c := tc.Claims
	c.Subject = tc.RegisteredClaims.Subject
	return c, nil
}

func keyMatches(k publicKey, alg string) bool {
	if k.alg != "" && k.alg != alg {
		return false
	}
	switch alg {
	case "RS256", "RS384", "RS512", "PS256":
		return k.kty == "RSA"
	case "ES256":
		return k.kty == "EC" && k.curve == "P-256"
	case "ES384":
		return k.kty == "EC" && k.curve == "P-384"
	}
	return false
}
