// Package auth validates Keycloak access tokens and maps their claims to a Principal.
package auth

import (
	"context"
	"strings"
)

// Role is an arch realm role.
type Role string

// The arch realm roles, lowest first.
const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

// orderedRoles is the order in which effective roles are listed.
var orderedRoles = []Role{RoleViewer, RoleEditor, RoleAdmin}

// Actor kinds.
const (
	ActorUser    = "user"
	ActorService = "service"
)

// Principal is the authenticated caller.
type Principal struct {
	Subject   string
	Name      string
	Email     string
	Roles     []Role
	Teams     []string
	ActorKind string
}

// Has reports whether the principal's effective roles include role.
func (p Principal) Has(role Role) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Claims are the token claims the API reads. Subject is filled from the registered `sub` claim.
type Claims struct {
	Subject           string      `json:"-"`
	Name              string      `json:"name"`
	PreferredUsername string      `json:"preferred_username"`
	Email             string      `json:"email"`
	ClientID          string      `json:"client_id"`
	AuthorizedParty   string      `json:"azp"`
	Teams             []string    `json:"teams"`
	RealmAccess       RealmAccess `json:"realm_access"`
}

// RealmAccess is the `realm_access` claim.
type RealmAccess struct {
	Roles []string `json:"roles"`
}

// EffectiveRoles expands realm roles: admin implies editor, editor implies viewer. Other role
// names are ignored. The result is always ordered viewer, editor, admin.
func EffectiveRoles(realmRoles []string) []Role {
	rank := -1
	for _, name := range realmRoles {
		for i, r := range orderedRoles {
			if name == string(r) && i > rank {
				rank = i
			}
		}
	}
	roles := make([]Role, 0, rank+1)
	return append(roles, orderedRoles[:rank+1]...)
}

// PrincipalFromClaims maps validated claims to a Principal. A token with a `client_id` claim, or a
// `service-account-` username, is a service account identified by its client id.
func PrincipalFromClaims(c Claims) Principal {
	p := Principal{
		Roles:     EffectiveRoles(c.RealmAccess.Roles),
		Teams:     c.Teams,
		ActorKind: ActorUser,
		Subject:   c.Subject,
		Name:      c.Name,
		Email:     c.Email,
	}
	if p.Teams == nil {
		p.Teams = []string{}
	}
	if c.ClientID != "" || strings.HasPrefix(c.PreferredUsername, "service-account-") {
		clientID := c.ClientID
		if clientID == "" {
			clientID = c.AuthorizedParty
		}
		p.ActorKind = ActorService
		p.Subject = clientID
		p.Name = clientID
		return p
	}
	if p.Name == "" {
		p.Name = c.PreferredUsername
	}
	return p
}

type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal in ctx, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
