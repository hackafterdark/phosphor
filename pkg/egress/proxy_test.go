package egress_test

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/stretchr/testify/require"
)

// startEchoServer runs a loopback http.Server that replies with the exact body it
// received, so a test can assert what the broker actually forwarded on the wire.
func startEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), func() { srv.Close() }
}

func TestRoundTripperResolvesAtAllowedHost(t *testing.T) {
	addr, stop := startEchoServer(t)
	defer stop()

	store, err := egress.NewSecretStore()
	require.NoError(t, err)
	tok, ok := store.Seal("ghp_LiveSecret12345", "github-pat")
	require.True(t, ok)

	rt := egress.NewRoundTripper(egress.Policy{HTTPSOnly: false, AllowedHosts: []string{"127.0.0.1"}}, store, nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: rt}

	resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+tok))
	require.NoError(t, err)
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "auth: ghp_LiveSecret12345", string(got),
		"the token must reach the allowlisted destination already resolved to plaintext")
}

func TestRoundTripperRefusesUnresolvableSentinel(t *testing.T) {
	addr, stop := startEchoServer(t)
	defer stop()

	store, _ := egress.NewSecretStore()
	rt := egress.NewRoundTripper(egress.Policy{HTTPSOnly: false, AllowedHosts: []string{"127.0.0.1"}}, store, nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: rt}

	// A syntactically-valid token this store never minted (wrong key) is unresolvable.
	rogue := "<secret@v1.github-pat.AAAAAAAAAAAAAA.end>"
	_, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+rogue))
	require.Error(t, err)
	require.Contains(t, err.Error(), egress.ErrUnresolvedSentinel.Error(),
		"a request carrying a token that will not open must be refused, not forwarded")
}

func TestRoundTripperDeniesNonAllowlistedHost(t *testing.T) {
	store, _ := egress.NewSecretStore()
	rt := egress.NewRoundTripper(egress.Policy{HTTPSOnly: false, AllowedHosts: []string{"api.github.com"}}, store, nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: rt}

	_, err := client.Get("http://127.0.0.1:1/")
	require.Error(t, err)
	require.Contains(t, err.Error(), "allowlist")
}

func TestProxyRequiresAuthAndDeniesByDefault(t *testing.T) {
	p, err := egress.NewProxy(egress.Policy{Enabled: true, HTTPSOnly: true, AllowedHosts: nil}, nil)
	require.NoError(t, err)
	defer p.Close()
	require.NotEmpty(t, p.AuthToken())

	// CONNECT to a host with no authorization is rejected with 401.
	require.Equal(t, "401", connectStatus(t, p, p.AuthToken(), false))
	// CONNECT with authorization to a host the (empty) allowlist does not name is 403.
	require.Equal(t, "403", connectStatus(t, p, p.AuthToken(), true))
}

func TestProxyConnectsOnlyToAllowlistedHost(t *testing.T) {
	// The broker tunnels only to an allowlisted destination. We allowlist the loopback
	// echo server's own address so the "allowed" CONNECT actually reaches a live
	// socket and gets a 200, while a host nobody named is refused before dialing.
	addr, stop := startEchoServer(t)
	defer stop()

	p, err := egress.NewProxy(egress.Policy{
		Enabled:      true,
		HTTPSOnly:    false, // a cleartext CONNECT target is fine to prove the gate
		AllowedHosts: []string{"127.0.0.1"},
	}, nil)
	require.NoError(t, err)
	defer p.Close()

	require.Equal(t, "200", connectStatus(t, p, p.AuthToken(), true, addr))
	require.Equal(t, "403", connectStatus(t, p, p.AuthToken(), true, "attacker.example.net:443"))
}

func TestProxyEnv(t *testing.T) {
	p, err := egress.NewProxy(egress.Policy{}, nil)
	require.NoError(t, err)
	defer p.Close()

	env := strings.Join(p.ProxyEnv(), "\n")
	require.Contains(t, env, "HTTP_PROXY=http://"+p.AuthToken()+"@"+p.Addr())
	require.Contains(t, env, "HTTPS_PROXY=http://"+p.AuthToken()+"@"+p.Addr())
	require.Contains(t, env, "PHOSPHOR_PROXY_AUTH="+p.AuthToken())
	require.NotContains(t, env, "snapcraft")
	require.Contains(t, env, "NO_PROXY=localhost,127.0.0.1,::1")
}

func TestProxyAuthorizationAcceptsStandardBasicForm(t *testing.T) {
	p, err := egress.NewProxy(egress.Policy{Enabled: true, AllowedHosts: nil}, nil)
	require.NoError(t, err)
	defer p.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", p.ProxyURL()+"/", strings.NewReader(""))
	require.NoError(t, err)
	req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.AuthToken()+":")))
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	req, err = http.NewRequest("GET", p.ProxyURL()+"/", strings.NewReader(""))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// connectStatus opens a raw connection to the broker, sends a CONNECT request (with
// or without the proxy-auth token) for a target, and returns the 3-digit status the
// broker answered with. It does not require the target to be reachable: the gate
// runs before dialing.
func connectStatus(t *testing.T, p *egress.Proxy, token string, withAuth bool, target ...string) string {
	t.Helper()
	host := "example.invalid:443"
	if len(target) > 0 && target[0] != "" {
		host = target[0]
	}
	conn, err := net.DialTimeout("tcp", p.Addr(), 5*time.Second)
	require.NoError(t, err)
	defer conn.Close()

	var b strings.Builder
	b.WriteString("CONNECT " + host + " HTTP/1.1\r\n")
	b.WriteString("Host: " + host + "\r\n")
	if withAuth {
		b.WriteString("Proxy-Authorization: Bearer " + token + "\r\n")
	}
	b.WriteString("\r\n")
	_, err = conn.Write([]byte(b.String()))
	require.NoError(t, err)

	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _ := conn.Read(buf)
	line := string(buf[:n])
	sp := strings.SplitN(line, " ", 3)
	if len(sp) >= 2 {
		return sp[1]
	}
	return ""
}

var _ = url.Parse

func useEgressBroker(t *testing.T) {
	t.Helper()
	require.NoError(t, egress.ResetForTest(true))
	egress.ResetBrokerForTest()
	t.Cleanup(func() {
		egress.ResetBrokerForTest()
		_ = egress.ResetForTest(false)
	})
}

func setEnvProxy(t *testing.T, target string) {
	t.Helper()
	t.Setenv("HTTP_PROXY", target)
	t.Setenv("HTTPS_PROXY", target)
	t.Setenv("http_proxy", target)
	t.Setenv("https_proxy", target)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
}

func TestProductionTransportResolvesSentinelWhenBrokerActive(t *testing.T) {
	useEgressBroker(t)
	addr, stop := startEchoServer(t)
	defer stop()

	tok, ok := egress.Seal("super-secret-123", "test")
	require.True(t, ok)
	require.NoError(t, egress.Start(egress.Policy{
		Enabled:      true,
		HTTPSOnly:    false,
		AllowedHosts: []string{"127.0.0.1"},
	}, false))

	client := &http.Client{Timeout: 5 * time.Second, Transport: egress.Transport(nil)}
	resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+tok))
	require.NoError(t, err)
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "auth: super-secret-123", string(got))
}

func TestProductionTransportBypassesEnvironmentProxyWhenBrokerActive(t *testing.T) {
	useEgressBroker(t)
	addr, stop := startEchoServer(t)
	defer stop()
	setEnvProxy(t, "http://127.0.0.1:1")

	tok, ok := egress.Seal("proxy-secret-456", "test")
	require.True(t, ok)
	require.NoError(t, egress.Start(egress.Policy{
		Enabled:      true,
		HTTPSOnly:    false,
		AllowedHosts: []string{"127.0.0.1"},
	}, false))

	client := &http.Client{Timeout: 5 * time.Second, Transport: egress.Transport(nil)}
	resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+tok))
	require.NoError(t, err)
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "auth: proxy-secret-456", string(got))
}

func TestRoundTripperFixedPolicyIgnoresEnvironmentProxy(t *testing.T) {
	addr, stop := startEchoServer(t)
	defer stop()
	setEnvProxy(t, "http://127.0.0.1:1")

	store, err := egress.NewSecretStore()
	require.NoError(t, err)
	tok, ok := store.Seal("fixed-policy-secret", "test")
	require.True(t, ok)

	rt := egress.NewRoundTripper(egress.Policy{
		HTTPSOnly:    false,
		AllowedHosts: []string{"127.0.0.1"},
	}, store, nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: rt}
	resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+tok))
	require.NoError(t, err)
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "auth: fixed-policy-secret", string(got))
}

func TestWrapClientIdempotent(t *testing.T) {
	wrapped := egress.WrapClient(&http.Client{Timeout: time.Second})
	again := egress.WrapClient(wrapped)
	require.Same(t, wrapped, again)
	_, ok := wrapped.Transport.(*egress.RoundTripperPolicy)
	require.True(t, ok)
}

func TestPolicyCheckContextRejectsResolvedPrivateHost(t *testing.T) {
	p := egress.Policy{
		HTTPSOnly:      true,
		DenyPrivateIPs: true,
		AllowedHosts:   []string{"private.example.com"},
		LookupHost: func(context.Context, string) ([]string, error) {
			return []string{"169.254.169.254"}, nil
		},
	}

	act, reason := p.CheckHostContext(t.Context(), "private.example.com:443")
	require.Equal(t, egress.Deny, act)
	require.Contains(t, reason, "refused private or reserved")
}

func TestPolicyCheckContextAllowsResolvedPublicHost(t *testing.T) {
	p := egress.Policy{
		HTTPSOnly:      true,
		DenyPrivateIPs: true,
		AllowedHosts:   []string{"public.example.com"},
		LookupHost: func(context.Context, string) ([]string, error) {
			return []string{"93.184.216.247"}, nil
		},
	}

	act, reason := p.CheckHostContext(t.Context(), "public.example.com:443")
	require.Equal(t, egress.Allow, act, reason)
}

func TestPolicyCheckContextRejectsEmptyResolution(t *testing.T) {
	p := egress.Policy{
		HTTPSOnly:      true,
		DenyPrivateIPs: true,
		AllowedHosts:   []string{"empty.example.com"},
		LookupHost: func(context.Context, string) ([]string, error) {
			return nil, nil
		},
	}

	act, reason := p.CheckHostContext(t.Context(), "empty.example.com:443")
	require.Equal(t, egress.Deny, act)
	require.Contains(t, reason, "resolved to no usable address")
}
