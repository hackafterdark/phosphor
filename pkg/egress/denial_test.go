package egress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDenialAdviceIncludesConfigFragment(t *testing.T) {
	t.Parallel()

	advice := DenialAdvice("api.example.com:443")
	require.Contains(t, advice, "api.example.com")
	require.Contains(t, advice, "allowed_hosts")
	require.Contains(t, advice, "egress_isolation")
	// The advisor must present itself as advisory, never self-applying.
	require.Contains(t, advice, "never writes config")
}

func TestDenialAdviceNonStandardPort(t *testing.T) {
	t.Parallel()

	advice := DenialAdvice("api.example.com:8443")
	require.Contains(t, advice, "api.example.com:8443")
}

func TestProxyDenialResponseCarriesAdvice(t *testing.T) {
	t.Parallel()

	ResetBrokerForTest()
	t.Cleanup(ResetBrokerForTest)

	p, err := NewProxy(Policy{
		Enabled:      true,
		HTTPSOnly:    false,
		AllowedHosts: []string{"allowed.example.com"},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	// CONNECT to a host outside the allowlist must carry the config
	// fragment in the denial body.
	req, err := http.NewRequest(http.MethodConnect, "http://blocked.example.com:443", nil)
	require.NoError(t, err)
	req.Header.Set(proxyAuthHeader, "Bearer "+p.AuthToken())

	rec := httptest.NewRecorder()
	p.serve(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "egress denied")
	require.Contains(t, body, "blocked.example.com")
	require.Contains(t, body, "allowed_hosts")
	require.Contains(t, body, "egress_isolation")
}

func TestProxyForwardDenialCarriesAdvice(t *testing.T) {
	t.Parallel()

	ResetBrokerForTest()
	t.Cleanup(ResetBrokerForTest)

	p, err := NewProxy(Policy{
		Enabled:      true,
		HTTPSOnly:    false,
		AllowedHosts: []string{"allowed.example.com"},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	req, err := http.NewRequest(http.MethodGet, "http://blocked.example.com/x", nil)
	require.NoError(t, err)
	req.Header.Set(proxyAuthHeader, "Bearer "+p.AuthToken())

	rec := httptest.NewRecorder()
	p.serve(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "egress denied")
	require.Contains(t, body, "blocked.example.com")
	require.True(t, strings.Contains(body, "allowed_hosts"),
		"denial body must carry the phosphor.json fragment")
}
