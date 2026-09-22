package mcp

import (
	"net/http"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/secrets"
	"github.com/hackafterdark/phosphor/pkg/security/urlguard"
	"github.com/stretchr/testify/require"
)

const mcpGuardToken = "Mcp9Zt4Kp7Qm2Bv8Ld6Hf1Rn3Gj5Yc0Xe"

func init() { secrets.Register(mcpGuardToken) }

// TestHeaderRoundTripper_RefusesSecretInURL proves the MCP HTTP/SSE transport refuses
// to dial a request whose URL carries a known credential, closing the exfiltration
// vector a repo-supplied MCP endpoint could otherwise exploit.
func TestHeaderRoundTripper_RefusesSecretInURL(t *testing.T) {
	rt := newHeaderRoundTripper(map[string]string{"Authorization": "Bearer public"})

	req, err := http.NewRequest("GET", "https://mcp.example.com/rpc?token="+mcpGuardToken, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	require.Error(t, err, "the MCP transport must refuse a URL carrying a known secret")
	require.ErrorContains(t, err, urlguard.ErrMessage)
	require.Nil(t, resp)
}

func TestHeaderRoundTripper_AllowsCleanURL(t *testing.T) {
	rt := newHeaderRoundTripper(nil)

	req, err := http.NewRequest("GET", "http://127.0.0.1:1/rpc?id=1", nil)
	require.NoError(t, err)

	// The guard passes (no secret in the URL); the request proceeds to the base
	// transport, which fails only on the connection/SSRF layer, never with a
	// credential-block message. That asymmetry is what proves the guard let it by.
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		require.NotContains(t, err.Error(), urlguard.ErrMessage)
	}
}
