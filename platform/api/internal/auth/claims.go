package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Claims is the identity extracted from a Keycloak JWT.
type Claims struct {
	Sub               string
	Email             string
	PreferredUsername string
	Department        string
	Groups            []string
	Roles             []string
}

type keycloakPayload struct {
	Sub         string   `json:"sub"`
	Email       string   `json:"email"`
	PreferredUN string   `json:"preferred_username"`
	Department  string   `json:"department"`
	Groups      []string `json:"groups"`
	Roles       []string `json:"roles"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

// DecodeJWT decodes the JWT payload WITHOUT verifying the signature. The
// browser validates the token against Keycloak; the backend trusts it. This
// matches the in-house llmgate/forge convention.
func DecodeJWT(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid token encoding")
	}
	var p keycloakPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, errors.New("invalid token payload")
	}
	if p.Sub == "" {
		return nil, errors.New("missing sub claim")
	}
	roles := append([]string{}, p.RealmAccess.Roles...)
	roles = append(roles, p.Roles...)
	for _, ra := range p.ResourceAccess {
		roles = append(roles, ra.Roles...)
	}
	return &Claims{
		Sub: p.Sub, Email: p.Email, PreferredUsername: p.PreferredUN,
		Department: p.Department, Groups: p.Groups, Roles: roles,
	}, nil
}

func (c *Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// PrimaryWorkspace is the user's personal workspace id, which is their sub.
func (c *Claims) PrimaryWorkspace() string { return c.Sub }
