package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/internal/config"
)

// TestWebFetchHardeningIPBlock demonstrates the IP-block policy rejects raw IPs.
func TestWebFetchHardeningIPBlock(t *testing.T) {
	t.Parallel()

	allowList = make(map[string]bool)
	client := &http.Client{Timeout: time.Second}

	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		t.Errorf("allowFn should not be called for raw IP")
		return false, nil
	}, nil)

	client.Transport = tr

	// Request to a raw IP (192.168.1.1) should fail.
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://192.168.1.1:8080/api", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		t.Fatal("expected error for raw IP request")
	}
	if resp != nil {
		defer resp.Body.Close()
	}
}

// TestWebFetchHardeningCIDRAllow demonstrates CIDR allow list is respected.
func TestWebFetchHardeningCIDRAllow(t *testing.T) {
	t.Parallel()

	allowList = make(map[string]bool)
	cfg := &config.Config{
		Tools: config.Tools{
			WebFetch: config.ToolWebFetch{
				IPAllowList: []string{"192.168.1.0/24"},
				AllowRawIPs: true,
			},
		},
	}

	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		t.Errorf("allowFn should not be called for CIDR-matched IP")
		return false, nil
	}, cfg)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	// Request to 192.168.1.100 (in /24 range) should succeed (no server running, but transport allows it).
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://192.168.1.100:8080/api", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		t.Fatal("expected network error for CIDR-matched IP (no server running)")
	}
	if resp != nil {
		defer resp.Body.Close()
	}
}

// TestWebFetchHardeningLocalhostAlwaysAllowed demonstrates 127.0.0.1 is always allowed.
func TestWebFetchHardeningLocalhostAlwaysAllowed(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	allowList = make(map[string]bool)
	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		t.Errorf("allowFn should not be called for localhost")
		return false, nil
	}, nil)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	req, err := http.NewRequestWithContext(t.Context(), "GET", ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected request to succeed, got: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %d", resp.StatusCode)
	}
	// localhost is always allowed by securityTransport.
	// If allowFn was called, the test fails.
}

// TestWebFetchHardeningUserPromptVerified demonstrates the TUI prompt is called when no match.
func TestWebFetchHardeningUserPromptVerified(t *testing.T) {
	t.Parallel()

	allowList = make(map[string]bool)
	called := false
	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		called = true
		return true, nil // Allow.
	}, nil)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	// Request to api.internal.dev (no config, not localhost) should prompt.
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://api.internal.dev/api", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		t.Fatal("expected network error for user-prompted request (no server running)")
	}
	if resp != nil {
		defer resp.Body.Close()
	}

	if !called {
		t.Error("expected allowFn to be called for unknown host")
	}
}

// TestWebFetchHardeningUserPromptDenyVerified demonstrates the prompt is called when no match.
func TestWebFetchHardeningUserPromptDenyVerified(t *testing.T) {
	t.Parallel()

	allowList = make(map[string]bool)
	called := false
	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		called = true
		return false, nil // Deny.
	}, nil)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	// Request to api.internal.dev (no config, not localhost) should prompt.
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://api.internal.dev/api", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		t.Fatal("expected error for denied request")
	}
	if resp != nil {
		defer resp.Body.Close()
	}

	if !called {
		t.Error("expected allowFn to be called for unknown host")
	}
}
