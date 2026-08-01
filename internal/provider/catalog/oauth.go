package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceFlow describes an RFC 8628 OAuth 2.0 Device Authorization Grant.
// Public clients (CLI tools) use this to let users authenticate in a browser
// without embedding a client secret.
type DeviceFlow struct {
	DeviceAuthURL string // POST here to request a device code
	TokenURL      string // POST here to poll for a token
	ClientID      string // public client identifier
	Scope         string // space-separated scopes
}

// DeviceCodeResponse is what the device authorization endpoint returns.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	// Some providers (GitHub) use a different field name.
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// TokenResponse is a successful token endpoint reply.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// tokenError is the error shape the token endpoint returns while the user
// has not yet authorized the device (or has denied it).
type tokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Start initiates the device flow: it POSTs to the device authorization
// endpoint and returns the codes the user needs.
func (f DeviceFlow) Start(ctx context.Context) (*DeviceCodeResponse, error) {
	form := url.Values{
		"client_id": {f.ClientID},
	}
	if f.Scope != "" {
		form.Set("scope", f.Scope)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.DeviceAuthURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("device flow: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("device flow: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device flow: http %d: %s", resp.StatusCode, snippet(body))
	}

	var dc DeviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("device flow: bad response: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, fmt.Errorf("device flow: server returned no device_code/user_code")
	}
	if dc.VerificationURI == "" {
		dc.VerificationURI = dc.VerificationURIComplete
	}
	if dc.Interval <= 0 {
		dc.Interval = 5 // RFC 8628 default
	}
	if dc.ExpiresIn <= 0 {
		dc.ExpiresIn = 900 // 15 minutes
	}
	return &dc, nil
}

// Poll polls the token endpoint until the user authorizes the device, the
// code expires, or the context is cancelled. It handles "authorization_pending"
// and "slow_down" per RFC 8628 §3.5.
func (f DeviceFlow) Poll(ctx context.Context, deviceCode string, interval int) (*TokenResponse, error) {
	if interval <= 0 {
		interval = 5
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}

		form := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {deviceCode},
			"client_id":   {f.ClientID},
		}

		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, f.TokenURL,
			strings.NewReader(form.Encode()))
		if err != nil {
			cancel()
			return nil, fmt.Errorf("device flow poll: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			cancel()
			// Transient network error — keep polling.
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()

		// Success.
		if resp.StatusCode == http.StatusOK {
			var tok TokenResponse
			if err := json.Unmarshal(body, &tok); err != nil {
				return nil, fmt.Errorf("device flow: bad token response: %w", err)
			}
			if tok.AccessToken != "" {
				return &tok, nil
			}
		}

		// Error — decide whether to retry or give up.
		var te tokenError
		if err := json.Unmarshal(body, &te); err != nil {
			return nil, fmt.Errorf("device flow: http %d: %s", resp.StatusCode, snippet(body))
		}
		switch te.Error {
		case "authorization_pending":
			// User hasn't authorized yet — keep polling.
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			return nil, fmt.Errorf("device code expired — restart the sign-in")
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		default:
			desc := te.ErrorDescription
			if desc == "" {
				desc = te.Error
			}
			return nil, fmt.Errorf("device flow: %s", desc)
		}
	}
}

// CopilotTokenExchange exchanges a GitHub OAuth access token for a short-lived
// GitHub Copilot API token. The Copilot API (api.githubcopilot.com) requires
// its own token, obtained from the internal endpoint.
func CopilotTokenExchange(ctx context.Context, githubToken string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/copilot_internal/v2/token", nil)
	if err != nil {
		return "", fmt.Errorf("copilot exchange: %w", err)
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "rick-cli")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("copilot exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("copilot exchange: http %d: %s", resp.StatusCode, snippet(body))
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("copilot exchange: bad response: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("copilot exchange: no token in response — does this account have Copilot?")
	}
	return result.Token, nil
}
