package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CliXiD/arch-api/internal/auth"
)

func TestEffectiveRoles(t *testing.T) {
	tests := []struct {
		name  string
		realm []string
		want  []auth.Role
	}{
		{"admin implies editor and viewer", []string{"admin"}, []auth.Role{"viewer", "editor", "admin"}},
		{"editor implies viewer", []string{"offline_access", "editor"}, []auth.Role{"viewer", "editor"}},
		{"viewer", []string{"default-roles-arch", "viewer"}, []auth.Role{"viewer"}},
		{"order is fixed", []string{"admin", "viewer", "editor"}, []auth.Role{"viewer", "editor", "admin"}},
		{"no arch role", []string{"default-roles-arch", "offline_access", "uma_authorization"}, []auth.Role{}},
		{"none", nil, []auth.Role{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, auth.EffectiveRoles(tt.realm))
		})
	}
}

func TestPrincipalFromClaims(t *testing.T) {
	claims := func(edit func(*auth.Claims)) auth.Claims {
		c := auth.Claims{Subject: "user-sub", PreferredUsername: "editor"}
		edit(&c)
		return c
	}
	tests := []struct {
		name   string
		claims auth.Claims
		want   auth.Principal
	}{
		{
			name: "editor with group teams",
			claims: claims(func(c *auth.Claims) {
				c.Name, c.Email = "Editor Local", "editor@example.test"
				c.Teams = []string{"platform"}
				c.RealmAccess.Roles = []string{"default-roles-arch", "editor"}
			}),
			want: auth.Principal{
				Subject: "user-sub", Name: "Editor Local", Email: "editor@example.test",
				Roles: []auth.Role{"viewer", "editor"}, Teams: []string{"platform"}, ActorKind: "user",
			},
		},
		{
			name:   "name falls back to preferred_username, teams and email empty",
			claims: claims(func(c *auth.Claims) { c.RealmAccess.Roles = []string{"viewer"} }),
			want: auth.Principal{
				Subject: "user-sub", Name: "editor", Email: "",
				Roles: []auth.Role{"viewer"}, Teams: []string{}, ActorKind: "user",
			},
		},
		{
			name: "client credentials token is a service",
			claims: auth.Claims{
				Subject: "svc-sub", ClientID: "arch-ci-platform", AuthorizedParty: "arch-ci-platform",
				PreferredUsername: "service-account-arch-ci-platform", Teams: []string{"platform"},
				RealmAccess: auth.RealmAccess{Roles: []string{"default-roles-arch", "editor"}},
			},
			want: auth.Principal{
				Subject: "arch-ci-platform", Name: "arch-ci-platform",
				Roles: []auth.Role{"viewer", "editor"}, Teams: []string{"platform"}, ActorKind: "service",
			},
		},
		{
			name: "service-account username without client_id is a user",
			claims: auth.Claims{
				Subject: "svc-sub", AuthorizedParty: "arch-ci-platform",
				PreferredUsername: "service-account-arch-ci-platform",
			},
			want: auth.Principal{
				Subject: "svc-sub", Name: "service-account-arch-ci-platform",
				Roles: []auth.Role{}, Teams: []string{}, ActorKind: "user",
			},
		},
		{
			name: "client_id different from azp is a user",
			claims: auth.Claims{
				Subject: "user-sub", Name: "Editor Local", ClientID: "arch-ci-platform", AuthorizedParty: "arch-web",
				PreferredUsername: "editor",
			},
			want: auth.Principal{
				Subject: "user-sub", Name: "Editor Local",
				Roles: []auth.Role{}, Teams: []string{}, ActorKind: "user",
			},
		},
		{
			name: "empty client_id with azp is a user",
			claims: auth.Claims{
				Subject: "user-sub", AuthorizedParty: "arch-web", PreferredUsername: "editor",
			},
			want: auth.Principal{
				Subject: "user-sub", Name: "editor",
				Roles: []auth.Role{}, Teams: []string{}, ActorKind: "user",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, auth.PrincipalFromClaims(tt.claims))
		})
	}
}
