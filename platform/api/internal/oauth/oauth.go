// Package oauth handles the connector OAuth dance.
//
// Each provider defines its authorize URL, token URL, and the small bits of
// per-provider behaviour (scope name, token field name, success response
// shape). The handler is generic; the registry is the data.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/store"
)

type Provider struct {
	ID            string   // "github", "slack"
	AuthURL       string   // authorize endpoint
	TokenURL      string   // code exchange endpoint
	DefaultScopes []string // when caller does not specify
	ClientID      string
	ClientSecret  string
}

func (p Provider) Configured() bool { return p.ClientID != "" && p.ClientSecret != "" }

type Manager struct {
	providers map[string]Provider
	creds     *store.CredentialStore
	baseURL   string // platform's own public URL, used to build the callback

	mu     sync.Mutex
	states map[string]pendingState
}

type pendingState struct {
	provider   string
	label      string
	scopes     []string
	userScopes []string
	userID     string
	createdAt  time.Time
}

func NewManager(creds *store.CredentialStore, baseURL string) *Manager {
	m := &Manager{
		providers: map[string]Provider{},
		creds:     creds,
		baseURL:   strings.TrimRight(baseURL, "/"),
		states:    map[string]pendingState{},
	}
	m.registerBuiltins()
	return m
}

func (m *Manager) registerBuiltins() {
	m.providers["github"] = Provider{
		ID:            "github",
		AuthURL:       "https://github.com/login/oauth/authorize",
		TokenURL:      "https://github.com/login/oauth/access_token",
		DefaultScopes: []string{"repo", "read:user"},
		ClientID:      os.Getenv("PLATFORM_GITHUB_CLIENT_ID"),
		ClientSecret:  os.Getenv("PLATFORM_GITHUB_CLIENT_SECRET"),
	}
	m.providers["slack"] = Provider{
		ID:            "slack",
		AuthURL:       "https://slack.com/oauth/v2/authorize",
		TokenURL:      "https://slack.com/api/oauth.v2.access",
		DefaultScopes: []string{"chat:write", "chat:write.public", "channels:read", "users:read"},
		ClientID:      os.Getenv("PLATFORM_SLACK_CLIENT_ID"),
		ClientSecret:  os.Getenv("PLATFORM_SLACK_CLIENT_SECRET"),
	}
}

func (m *Manager) ProvidersJSON() []map[string]any {
	out := make([]map[string]any, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, map[string]any{
			"id":             p.ID,
			"configured":     p.Configured(),
			"default_scopes": p.DefaultScopes,
		})
	}
	return out
}

// Start kicks off the dance: produces the authorize URL the user should
// visit and a CSRF-resistant state token that is later matched in Callback.
func (m *Manager) Start(providerID, label, userID string, scopes []string) (string, error) {
	return m.StartWithUserScopes(providerID, label, userID, scopes, nil)
}

// StartWithUserScopes additionally requests user_scope (xoxp- on Slack). When
// userScopes is empty, behaves identically to Start.
func (m *Manager) StartWithUserScopes(providerID, label, userID string, scopes, userScopes []string) (string, error) {
	p, ok := m.providers[providerID]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerID)
	}
	if !p.Configured() {
		return "", fmt.Errorf("provider %q has no OAuth credentials: set PLATFORM_%s_CLIENT_ID / PLATFORM_%s_CLIENT_SECRET",
			providerID, strings.ToUpper(providerID), strings.ToUpper(providerID))
	}
	if len(scopes) == 0 {
		scopes = p.DefaultScopes
	}
	state := randomState()
	m.mu.Lock()
	m.states[state] = pendingState{
		provider: providerID, label: label, scopes: scopes,
		userScopes: userScopes, userID: userID, createdAt: time.Now(),
	}
	m.mu.Unlock()

	q := url.Values{}
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", m.callbackURL(providerID))
	q.Set("scope", strings.Join(scopes, providerScopeSeparator(providerID)))
	if len(userScopes) > 0 {
		q.Set("user_scope", strings.Join(userScopes, providerScopeSeparator(providerID)))
	}
	q.Set("state", state)
	if providerID == "github" {
		q.Set("allow_signup", "false")
	}
	return p.AuthURL + "?" + q.Encode(), nil
}

// Callback handles the redirect from the OAuth provider, exchanges the code
// for a token, persists it as a Credential, and returns the saved credential
// so the handler can render a self-closing success page. For Slack, the
// user-level token (authed_user.access_token) is also stored under the
// `slack-user` connector when the grant included user scopes.
func (m *Manager) Callback(ctx context.Context, providerID string, query url.Values) (*store.Credential, error) {
	state := query.Get("state")
	m.mu.Lock()
	pending, ok := m.states[state]
	delete(m.states, state)
	m.mu.Unlock()
	if !ok {
		return nil, errors.New("unknown or expired state")
	}
	if pending.provider != providerID {
		return nil, errors.New("provider mismatch")
	}
	if code := query.Get("code"); code == "" {
		return nil, fmt.Errorf("oauth error: %s — %s", query.Get("error"), query.Get("error_description"))
	}

	p := m.providers[providerID]
	token, scopes, userToken, userScopes, err := m.exchange(ctx, p, query.Get("code"))
	if err != nil {
		return nil, err
	}

	label := pending.label
	if label == "" {
		label = "oauth"
	}
	cred, err := m.creds.Upsert(&store.Credential{
		UserID:    pending.userID,
		Connector: providerID,
		Label:     label,
		Secret:    token,
		Scopes:    scopes,
	})
	if err != nil {
		return nil, err
	}

	// Store user token under a parallel connector key so workflows can
	// choose: PLATFORM_TOKEN_SLACK (bot) vs PLATFORM_TOKEN_SLACK_USER.
	if userToken != "" {
		_, _ = m.creds.Upsert(&store.Credential{
			UserID:    pending.userID,
			Connector: providerID + "-user",
			Label:     label,
			Secret:    userToken,
			Scopes:    userScopes,
		})
	}
	return cred, nil
}

func (m *Manager) callbackURL(providerID string) string {
	if m.baseURL == "" {
		return "http://localhost:8787/oauth/" + providerID + "/callback"
	}
	return m.baseURL + "/oauth/" + providerID + "/callback"
}

func (m *Manager) exchange(ctx context.Context, p Provider, code string) (string, []string, string, []string, error) {
	form := url.Values{}
	form.Set("client_id", p.ClientID)
	form.Set("client_secret", p.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", m.callbackURL(p.ID))

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, "", nil, err
	}
	defer resp.Body.Close()

	switch p.ID {
	case "github":
		var body struct {
			AccessToken string `json:"access_token"`
			Scope       string `json:"scope"`
			Error       string `json:"error"`
			ErrorDesc   string `json:"error_description"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", nil, "", nil, err
		}
		if body.Error != "" {
			return "", nil, "", nil, fmt.Errorf("github token: %s — %s", body.Error, body.ErrorDesc)
		}
		return body.AccessToken, strings.Split(body.Scope, ","), "", nil, nil
	case "slack":
		var body struct {
			OK          bool   `json:"ok"`
			Error       string `json:"error"`
			AccessToken string `json:"access_token"`
			Scope       string `json:"scope"`
			AuthedUser  struct {
				AccessToken string `json:"access_token"`
				Scope       string `json:"scope"`
			} `json:"authed_user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", nil, "", nil, err
		}
		if !body.OK {
			return "", nil, "", nil, fmt.Errorf("slack token: %s", body.Error)
		}
		var userScopes []string
		if body.AuthedUser.Scope != "" {
			userScopes = strings.Split(body.AuthedUser.Scope, ",")
		}
		return body.AccessToken, strings.Split(body.Scope, ","), body.AuthedUser.AccessToken, userScopes, nil
	}
	return "", nil, "", nil, fmt.Errorf("unknown provider %q", p.ID)
}

func providerScopeSeparator(provider string) string {
	if provider == "slack" {
		return ","
	}
	return " "
}

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
