// Package xai provides xAI (Grok) OAuth integration using the standard
// RFC 8628 device-code flow against xAI's OAuth endpoints.
package xai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/internal/oauth"
)

const userAgent = "phosphor/1.0"

// scopes requested during device-code login. offline_access yields a
// refresh token; api:access grants the inference API surface.
const scopes = "openid profile email offline_access grok-cli:access api:access"

// ErrNotAvailable is returned when the account is authenticated but the
// API surface is blocked (e.g. tier-gated with HTTP 403).
var ErrNotAvailable = errors.New("xAI API not available for this account")

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// RequestDeviceCode initiates the device-code flow with xAI.
func RequestDeviceCode(ctx context.Context) (*DeviceCode, error) {
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("scope", scopes)

	req, err := http.NewRequestWithContext(ctx, "POST", deviceCodeURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed: %s - %s", resp.Status, string(body))
	}

	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

// PollForToken polls xAI for the access token after user authorization.
func PollForToken(ctx context.Context, dc *DeviceCode) (*oauth.Token, error) {
	interval := max(dc.Interval, 5)
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		token, err := tryGetToken(ctx, dc.DeviceCode)
		if err == errPending {
			continue
		}
		if err == errSlowDown {
			interval += 5
			ticker.Reset(time.Duration(interval) * time.Second)
			continue
		}
		if err != nil {
			return nil, err
		}
		return token, nil
	}

	return nil, fmt.Errorf("authorization timed out")
}

var (
	errPending  = fmt.Errorf("pending")
	errSlowDown = fmt.Errorf("slow_down")
)

func tryGetToken(ctx context.Context, deviceCode string) (*oauth.Token, error) {
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("device_code", deviceCode)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, "POST", accessTokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// The device-flow errors (authorization_pending, slow_down) are carried
	// in the body with an HTTP 400, so decide on the OAuth error field, not
	// the status alone. Anything else — a 503 gateway page, a 400 with an
	// unrecognized error — is a real failure, not something to keep polling.
	switch result.Error {
	case "":
		if result.AccessToken == "" {
			return nil, fmt.Errorf("token exchange failed: %s (status %d)", resp.Status, resp.StatusCode)
		}
		token := &oauth.Token{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
			ExpiresIn:    result.ExpiresIn,
		}
		token.SetExpiresAt()
		return token, nil
	case "authorization_pending":
		return nil, errPending
	case "slow_down":
		return nil, errSlowDown
	default:
		return nil, fmt.Errorf("authorization failed: %s", result.Error)
	}
}

// RefreshToken exchanges the refresh token for a fresh access token.
func RefreshToken(ctx context.Context, refreshToken string) (*oauth.Token, error) {
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", accessTokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrNotAvailable
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s - %s", resp.Status, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	token := &oauth.Token{
		AccessToken: result.AccessToken,
		// RFC 6749 §6: a refresh response may omit a new refresh token.
		// Retain the original so future refreshes still have a credential.
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	token.SetExpiresAt()
	return token, nil
}
