package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/stretchr/testify/require"
)

// TestWebFetchHardeningIPBlock demonstrates the IP-block policy rejects raw IPs.
func TestWebFetchHardeningIPBlock(t *testing.T) {
	t.Parallel()

	resetAllowList()
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

	resetAllowList()
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

	resetAllowList()
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

	resetAllowList()
	called := false
	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		called = true
		return true, nil // Allow.
	}, nil)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	// Unique host per test: the package-level allow list is shared state and
	// these tests run in parallel, so reusing a host across tests lets one
	// test's approval leak into another's trust check. .test is reserved and
	// never resolves.
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://api.allow-prompt.test/api", nil)
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

	resetAllowList()
	called := false
	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		called = true
		return false, nil // Deny.
	}, nil)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	// Unique host per test: the package-level allow list is shared state and
	// these tests run in parallel, so reusing a host across tests lets one
	// test's approval leak into another's trust check. .test is reserved and
	// never resolves.
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://api.deny-prompt.test/api", nil)
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

// TestWebFetchHardeningIPv6LiteralBlocked demonstrates bracketed IPv6 literals
// are treated as raw IPs and fail closed without reaching the trust hierarchy.
func TestWebFetchHardeningIPv6LiteralBlocked(t *testing.T) {
	client := &http.Client{Timeout: time.Second}

	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		t.Errorf("allowFn should not be called for raw IPv6 literal, got host %q", host)
		return false, nil
	}, nil)

	client.Transport = tr

	// Bracketed IPv6 literals must be blocked like raw IPv4s.
	for _, target := range []string{
		"http://[::1]:6379/",
		"http://[fd00::dead:beef]/",
		"http://[2001:db8::1]:8443/api",
	} {
		req, err := http.NewRequestWithContext(t.Context(), "GET", target, nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Fatalf("expected error for raw IPv6 literal %s", target)
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
}

// TestWebFetchHardeningIPv6LocalhostAllowed demonstrates [::1] is treated as
// localhost and allowed through without a prompt.
func TestWebFetchHardeningIPv6LocalhostAllowed(t *testing.T) {
	tr := newSecurityTransport(http.DefaultTransport, func(ctx context.Context, host string) (bool, error) {
		t.Errorf("allowFn should not be called for IPv6 localhost")
		return false, nil
	}, nil)

	client := &http.Client{Timeout: time.Second}
	client.Transport = tr

	// Request to [::1] should pass the transport gate. The dial itself may
	// fail if IPv6 loopback is unavailable, but the failure must not be the
	// raw-IP block.
	req, err := http.NewRequestWithContext(t.Context(), "GET", "http://[::1]:6379/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil && strings.Contains(err.Error(), "raw IP") {
		t.Fatalf("IPv6 localhost must not be blocked as raw IP, got: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
}

// TestWebFetchDialGateResolvedAddress is the DNS-rebinding regression table:
// an approved hostname whose DNS answer lands in a denied range must be
// refused at the socket, while loopback, config-allowlisted, and public
// addresses pass.
func TestWebFetchDialGateResolvedAddress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		address string
		allowed bool
	}{
		{"127.0.0.1:8080", true},      // loopback always allowed
		{"[::1]:6379", true},          // IPv6 loopback allowed
		{"169.254.169.254:80", false}, // cloud metadata
		{"10.0.0.5:443", false},       // RFC1918
		{"172.16.0.1:80", false},      // RFC1918
		{"192.168.1.5:80", false},     // RFC1918
		{"100.64.0.1:80", false},      // CGNAT
		{"[fd00::1]:443", false},      // IPv6 ULA
		{"[fe80::1]:80", false},       // IPv6 link-local
		{"93.184.216.34:443", true},   // public
		{"[2001:db8::1]:443", true},   // public IPv6 (example range, unrouted)
	}
	for _, c := range cases {
		got := dialAddressAllowed(c.address, nil)
		if got != c.allowed {
			t.Errorf("dialAddressAllowed(%q) = %v, want %v", c.address, got, c.allowed)
		}
	}
}

// TestWebFetchDialGateHonorsConfigOverrides verifies the gate applies the same
// exemptions as the raw-IP policy: allow_raw_ips and ip_allow_list CIDRs.
func TestWebFetchDialGateHonorsConfigOverrides(t *testing.T) {
	t.Parallel()

	rawIPs := &config.Config{
		Tools: config.Tools{
			WebFetch: config.ToolWebFetch{AllowRawIPs: true},
		},
	}
	require.True(t, dialAddressAllowed("10.0.0.5:443", rawIPs),
		"allow_raw_ips must exempt the dial gate, matching the raw-IP policy")

	cidrCfg := &config.Config{
		Tools: config.Tools{
			WebFetch: config.ToolWebFetch{IPAllowList: []string{"192.168.1.0/24"}},
		},
	}
	require.True(t, dialAddressAllowed("192.168.1.5:8080", cidrCfg),
		"allow-listed CIDR must pass the dial gate")
	require.False(t, dialAddressAllowed("10.0.0.5:8080", cidrCfg),
		"addresses outside the allow list must stay denied")
}

// TestWebFetchDialGateLocalhostHostnameAllowed verifies the default gate keeps
// legitimate local dev flows working: an httptest server on 127.0.0.1 must
// still be reachable through the guarded dialer.
func TestWebFetchDialGateLocalhostHostnameAllowed(t *testing.T) {
	// t.Setenv makes the test non-parallel by design.

	// Ensure no env proxy interferes with the gate in either direction.
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("all_proxy", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resetAllowList()
	transport := egress.NewHTTPTransport()
	transport.DialContext = guardedDialer(nil, transport.Proxy).DialContext
	client := &http.Client{
		Timeout: time.Second,
		Transport: newSecurityTransport(transport, func(ctx context.Context, host string) (bool, error) {
			t.Errorf("allowFn should not be called for localhost, got host %q", host)
			return false, nil
		}, nil),
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("localhost hostname must pass the dial gate, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %d", resp.StatusCode)
	}
}
