package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SlackProvisionRequest mirrors what `apps.manifest.create` takes.
type SlackProvisionRequest struct {
	AppConfigToken string   `json:"appConfigToken"`
	AppName        string   `json:"appName,omitempty"`
	BotName        string   `json:"botName,omitempty"`
	RedirectURL    string   `json:"redirectUrl,omitempty"`
	BotScopes      []string `json:"botScopes,omitempty"`
}

type SlackProvisionResult struct {
	AppID             string   `json:"app_id"`
	ClientID          string   `json:"client_id"`
	ClientSecret      string   `json:"client_secret"`
	SigningSecret     string   `json:"signing_secret"`
	Verification      string   `json:"verification_token"`
	Scopes            []string `json:"scopes"`
	OAuthAuthorizeURL string   `json:"oauth_authorize_url"`
}

// ProvisionSlackApp uses a Slack app-configuration token (xoxe.xoxp-…) to
// create an OAuth app via `apps.manifest.create`, then registers the issued
// client_id/secret with the running platform so the "Connect with slack"
// button immediately works.
func (m *Manager) ProvisionSlackApp(ctx context.Context, req SlackProvisionRequest) (*SlackProvisionResult, error) {
	if req.AppConfigToken == "" {
		return nil, errors.New("appConfigToken required")
	}
	if !strings.HasPrefix(req.AppConfigToken, "xoxe.") {
		return nil, errors.New("appConfigToken must be a Slack app-config token (xoxe.xoxp-…)")
	}

	appName := req.AppName
	if appName == "" {
		appName = "Workflow Platform (nk)"
	}
	botName := req.BotName
	if botName == "" {
		botName = "platform-agent"
	}
	redirect := req.RedirectURL
	if redirect == "" {
		redirect = strings.TrimRight(m.callbackURL("slack"), "/")
	}
	scopes := req.BotScopes
	if len(scopes) == 0 {
		scopes = defaultBotScopes()
	}

	manifest := map[string]any{
		"display_information": map[string]any{
			"name":             appName,
			"description":      "Personal automation platform.",
			"background_color": "#1c1c1f",
		},
		"features": map[string]any{
			"bot_user": map[string]any{
				"display_name":  botName,
				"always_online": true,
			},
		},
		"oauth_config": map[string]any{
			"redirect_urls": []string{redirect},
			"scopes": map[string]any{
				"bot": scopes,
			},
		},
		"settings": map[string]any{
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    false,
			"token_rotation_enabled": false,
		},
	}
	manifestJSON, _ := json.Marshal(manifest)

	body, _ := json.Marshal(map[string]any{"manifest": string(manifestJSON)})
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/apps.manifest.create", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+req.AppConfigToken)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("slack call: %w", err)
	}
	defer resp.Body.Close()

	var sresp struct {
		OK              bool   `json:"ok"`
		Error           string `json:"error"`
		AppID           string `json:"app_id"`
		Credentials     struct {
			ClientID          string `json:"client_id"`
			ClientSecret      string `json:"client_secret"`
			VerificationToken string `json:"verification_token"`
			SigningSecret     string `json:"signing_secret"`
		} `json:"credentials"`
		OAuthAuthorizeURL string `json:"oauth_authorize_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sresp); err != nil {
		return nil, fmt.Errorf("decode slack response: %w", err)
	}
	if !sresp.OK {
		return nil, fmt.Errorf("slack apps.manifest.create failed: %s", sresp.Error)
	}

	// Register the issued credentials with the running platform.
	m.mu.Lock()
	p := m.providers["slack"]
	p.ClientID = sresp.Credentials.ClientID
	p.ClientSecret = sresp.Credentials.ClientSecret
	m.providers["slack"] = p
	m.mu.Unlock()

	// Best-effort: persist into .env so a restart keeps the wiring.
	_ = persistEnv(map[string]string{
		"PLATFORM_SLACK_CLIENT_ID":     sresp.Credentials.ClientID,
		"PLATFORM_SLACK_CLIENT_SECRET": sresp.Credentials.ClientSecret,
	})

	return &SlackProvisionResult{
		AppID:             sresp.AppID,
		ClientID:          sresp.Credentials.ClientID,
		ClientSecret:      sresp.Credentials.ClientSecret,
		SigningSecret:     sresp.Credentials.SigningSecret,
		Verification:      sresp.Credentials.VerificationToken,
		Scopes:            scopes,
		OAuthAuthorizeURL: sresp.OAuthAuthorizeURL,
	}, nil
}

func defaultBotScopes() []string {
	return []string{
		"chat:write", "chat:write.public", "chat:write.customize",
		"channels:read", "channels:history", "channels:join",
		"groups:read", "groups:history", "groups:write",
		"im:read", "im:write", "im:history",
		"mpim:read", "mpim:write", "mpim:history",
		"users:read", "users:read.email",
		"reactions:read", "reactions:write",
		"app_mentions:read",
		"files:read", "files:write",
		"emoji:read", "team:read", "links:read", "links:write",
	}
}

// User scopes — let workflows post as the human ("Namkyu Cho") rather than as
// the bot user. Same Slack API surface, just authorized as the user.
func defaultUserScopes() []string {
	return []string{
		"chat:write",
		"channels:read", "channels:history", "channels:write",
		"groups:read", "groups:history", "groups:write",
		"im:read", "im:write", "im:history",
		"mpim:read", "mpim:write", "mpim:history",
		"users:read", "users:read.email",
		"reactions:read", "reactions:write",
		"search:read",
		"files:read", "files:write",
	}
}

// SlackUpdateScopesRequest refreshes the Slack app manifest so the next OAuth
// grant carries the platform's full default scope set.
type SlackUpdateScopesRequest struct {
	AppConfigToken string `json:"appConfigToken"`
	AppID          string `json:"appId,omitempty"`
}

type SlackUpdateScopesResult struct {
	AppID          string   `json:"app_id"`
	Scopes         []string `json:"scopes"`
	ReinstallURL   string   `json:"reinstall_url"`
}

func (m *Manager) UpdateSlackAppScopes(ctx context.Context, req SlackUpdateScopesRequest) (*SlackUpdateScopesResult, error) {
	if req.AppConfigToken == "" {
		return nil, errors.New("appConfigToken required")
	}
	appID := req.AppID
	if appID == "" {
		appID = os.Getenv("PLATFORM_SLACK_APP_ID")
	}
	if appID == "" {
		return nil, errors.New("appID required: pass body.appId or set PLATFORM_SLACK_APP_ID")
	}

	redirect := strings.TrimRight(m.callbackURL("slack"), "/")
	scopes := defaultBotScopes()
	userScopes := defaultUserScopes()

	manifest := map[string]any{
		"display_information": map[string]any{
			"name":             "Workflow Platform (nk)",
			"description":      "Personal automation platform.",
			"background_color": "#1c1c1f",
		},
		"features": map[string]any{
			"bot_user": map[string]any{
				"display_name":  "platform-agent",
				"always_online": true,
			},
		},
		"oauth_config": map[string]any{
			"redirect_urls": []string{redirect},
			"scopes": map[string]any{
				"bot":  scopes,
				"user": userScopes,
			},
		},
		"settings": map[string]any{
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    false,
			"token_rotation_enabled": false,
		},
	}
	manifestJSON, _ := json.Marshal(manifest)
	body, _ := json.Marshal(map[string]any{
		"app_id":   appID,
		"manifest": string(manifestJSON),
	})

	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/apps.manifest.update", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+req.AppConfigToken)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var sresp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sresp); err != nil {
		return nil, err
	}
	if !sresp.OK {
		return nil, fmt.Errorf("apps.manifest.update: %s", sresp.Error)
	}

	// Build a fresh OAuth start URL with the new scope set, including the
	// user_scope param so the grant also issues an xoxp- token authorising
	// API calls as the human (not the bot).
	label := "default"
	startURL, err := m.StartWithUserScopes("slack", label, "u_local", scopes, userScopes)
	if err != nil {
		return nil, err
	}

	return &SlackUpdateScopesResult{
		AppID:        appID,
		Scopes:       scopes,
		ReinstallURL: startURL,
	}, nil
}

// persistEnv updates (or appends) keys in the project's .env so the values
// survive an API restart. Best effort: errors are swallowed.
func persistEnv(updates map[string]string) error {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, ".env"),
		filepath.Join(cwd, "..", ".env"),
		filepath.Join(cwd, "..", "..", ".env"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		return errors.New(".env not found")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		for k, v := range updates {
			if strings.HasPrefix(line, k+"=") {
				lines[i] = k + "=" + v
				seen[k] = true
			}
		}
	}
	for k, v := range updates {
		if !seen[k] {
			lines = append(lines, k+"="+v)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
