package egress_test

import (
	"net/url"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/stretchr/testify/require"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

func TestPolicyDeniesByDefault(t *testing.T) {
	t.Parallel()
	p := egress.Policy{Enabled: true, HTTPSOnly: true}
	act, _ := p.Check(mustURL(t, "https://api.example.com/x"))
	require.Equal(t, egress.Deny, act, "an empty allowlist permits no destination")
}

func TestPolicyHTTPSOntly(t *testing.T) {
	t.Parallel()
	p := egress.Policy{HTTPSOnly: true, AllowedHosts: []string{"example.com"}}
	act, _ := p.Check(mustURL(t, "http://example.com/x"))
	require.Equal(t, egress.Deny, act, "cleartext must be refused when HTTPSOnly is on")

	p.HTTPSOnly = false
	act, _ = p.Check(mustURL(t, "http://example.com/x"))
	require.Equal(t, egress.Allow, act)
}

func TestPolicyHostMatching(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		target  string
		want    egress.Action
	}{
		{"api.example.com", "https://api.example.com", egress.Allow},
		{"api.example.com", "https://other.example.com", egress.Deny},
		{"*.example.com", "https://api.example.com", egress.Allow},
		{"*.example.com", "https://example.com", egress.Deny},             // bare zone is not a subdomain hit
		{"*.example.com", "https://a.b.example.com", egress.Deny},         // only one left label
		{"example.com", "https://example.com", egress.Allow},              // bare domain matches itself
		{"example.com", "https://deep.example.com", egress.Allow},         // and its subdomains
		{"EXAMPLE.com", "https://Deep.Example.COM", egress.Allow},         // case-insensitive
		{"api.example.com:8443", "https://api.example.com", egress.Allow}, // port stripped in pattern
	}
	for _, c := range cases {
		p := egress.Policy{HTTPSOnly: true, AllowedHosts: []string{c.pattern}}
		act, reason := p.Check(mustURL(t, c.target))
		require.Equal(t, c.want, act, "pattern=%q target=%q reason=%q", c.pattern, c.target, reason)
	}
}

func TestPolicyPrivateIPDenial(t *testing.T) {
	t.Parallel()
	p := egress.Policy{
		HTTPSOnly:      true,
		DenyPrivateIPs: true,
		AllowedHosts:   []string{"169.254.169.254", "10.0.0.5", "8.8.8.8"},
	}
	act, _ := p.Check(mustURL(t, "https://169.254.169.254/latest/meta"))
	require.Equal(t, egress.Deny, act, "cloud-metadata endpoint is refused")
	act, _ = p.Check(mustURL(t, "https://10.0.0.5/"))
	require.Equal(t, egress.Deny, act, "private range is refused")

	// A public literal IP that is allowlisted is still permitted.
	act, _ = p.Check(mustURL(t, "https://8.8.8.8/"))
	require.Equal(t, egress.Allow, act)
}

func TestPolicyCheckHost(t *testing.T) {
	t.Parallel()
	p := egress.Policy{HTTPSOnly: true, AllowedHosts: []string{"github.com"}}
	act, _ := p.CheckHost("github.com:443")
	require.Equal(t, egress.Allow, act)
	act, _ = p.CheckHost("attacker.example.net:443")
	require.Equal(t, egress.Deny, act)
	act, _ = p.CheckHost("github.com:80")
	require.Equal(t, egress.Deny, act)
	act, _ = p.CheckHost("127.0.0.1:443")
	require.Equal(t, egress.Deny, act)
}

func TestPolicyControlContextRejectsResolvedPrivateAddresses(t *testing.T) {
	t.Parallel()
	p := egress.Policy{HTTPSOnly: true, DenyPrivateIPs: true, AllowedHosts: []string{"metadata.example.com"}}
	err := p.ControlContext(t.Context(), "tcp", "169.254.169.254:443", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private or reserved")
	require.NoError(t, p.ControlContext(t.Context(), "tcp", "93.184.216.247:443", nil))
}
