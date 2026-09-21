package tools

import (
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"net"
	"net/http"

	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/stretchr/testify/require"
)

// Phase 11 integration coverage. These tests connect the read-path redaction to
// the opt-in egress-isolation tier: a detected credential is sealed into an
// AES-256-GCM token, that token is inert to the detector (so it never blocks a
// legitimate write), and it resolves back to the exact plaintext only inside the
// broker, at an allowlisted hop. Like the other redaction tests these mutate
// process-global state and therefore do not run in parallel.

var egressSealRe = regexp.MustCompile(`<secret@v1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.end>`)

// useEgress seeds a fresh, enabled secret store for one test and restores a
// disabled store on cleanup. The store is process-global, so a test that seals
// must opt in explicitly; tests that never call this see the tier off.
func useEgress(t *testing.T) {
	t.Helper()
	require.NoError(t, egress.ResetForTest(true))
	egress.ResetBrokerForTest()
	t.Cleanup(func() {
		egress.ResetBrokerForTest()
		_ = egress.ResetForTest(false)
	})
}

// The gitleaks-detected fixture every read-path test in this package shares.
const egressDetectedKey = "AKIAIOSVPK253PIP5TGP"

// (a) A read-path finding becomes a sealed, broker-resolvable token.
func TestEgressSeal_ReadPathEmitsResolvableSentinels(t *testing.T) {
	useEgress(t)
	useRedaction(t, RedactionPolicyOptions{SealSentinelsEnabled: ptrBool(true)})

	got := redactSecretsAt("AWS_ACCESS_KEY_ID=\""+egressDetectedKey+"\"", "deploy/app.env", "view")

	require.NotContains(t, got, egressDetectedKey, "the plaintext credential must never survive into the transcript")
	require.NotContains(t, got, "<redacted:gitleaks:", "the sealed tier replaces the plain sentinel, it does not append to it")

	m := egressSealRe.FindString(got)
	require.NotEmpty(t, m, "a sealed egress token must be present")
	require.True(t, strings.HasPrefix(m, "<secret@v1."), "sealed tokens live in their own namespace")

	plain, ok := egress.Open(m)
	require.True(t, ok, "the broker-side store opens the token it minted")
	require.Equal(t, egressDetectedKey, plain, "the token round-trips to the exact credential")
}

// (b) The write gate defangs our own sealed tokens but still blocks a verbatim key.
func TestEgressSeal_WriteGateIgnoresSealedTokens(t *testing.T) {
	useEgress(t)

	tok, ok := egress.Seal(egressDetectedKey, "aws-access-token")
	require.True(t, ok)

	// Copying the inert handle into a file is a legitimate round-trip, not a leak.
	require.NoError(t, checkSecretsAt("AWS_ACCESS_KEY_ID=\""+tok+"\"", "deploy/app.env"),
		"a sealed token is our own handle and must not trip the write gate")

	// Control: the real credential typed verbatim is still caught.
	require.Error(t, checkSecretsAt("AWS_ACCESS_KEY_ID=\""+egressDetectedKey+"\"", "deploy/app.env"),
		"a credential the model typed directly has no token form and must still be blocked")
}

// (c) With the tier left off, shipped read output is byte-for-byte the plain sentinel.
func TestEgressSeal_DefaultOffKeepsStaticSentinels(t *testing.T) {
	// Store seeded and enabled (proving the gate is the snapshot, not key availability),
	// but the redaction snapshot never armed the sealed path (the shipped default).
	useEgress(t)
	useRedaction(t, RedactionPolicyOptions{})
	require.False(t, SealSentinelsEnabled())

	got := redactSecretsAt("AWS_ACCESS_KEY_ID=\""+egressDetectedKey+"\"", "deploy/app.env", "view")
	require.NotContains(t, got, egressDetectedKey)
	require.Contains(t, got, "<redacted:gitleaks:", "off means the plain non-reusable sentinel, exactly as shipped")
	require.NotContains(t, got, "<secret@v1.")
}

// (d) A token issued by the redaction path resolves only at the broker, end-to-end.
func TestEgressSeal_RoundTripperResolvesAtAllowlistedHop(t *testing.T) {
	useEgress(t)
	useRedaction(t, RedactionPolicyOptions{SealSentinelsEnabled: ptrBool(true)})

	addr, stop := startEgressEcho(t)
	defer stop()

	// Produce the token the way the real read path does, then drive it through the
	// broker transport the web tools would use when the tier is on.
	red := redactSecretsAt("AWS_ACCESS_KEY_ID=\""+egressDetectedKey+"\"", "deploy/app.env", "view")
	tok := egressSealRe.FindString(red)
	require.NotEmpty(t, tok)

	rt := egress.NewRoundTripper(egress.Policy{HTTPSOnly: false, AllowedHosts: []string{"127.0.0.1"}}, egress.Store(), nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: rt}

	resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+tok))
	require.NoError(t, err)
	defer resp.Body.Close()
	echoed, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "auth: "+egressDetectedKey, string(echoed),
		"the allowlisted destination receives the resolved plaintext and nothing else")
}

// (d-bis) An unresolvable token is refused rather than forwarded as an opaque blob.
func TestEgressSeal_RoundTripperRefusesUnresolvable(t *testing.T) {
	useEgress(t)

	addr, stop := startEgressEcho(t)
	defer stop()

	rt := egress.NewRoundTripper(egress.Policy{HTTPSOnly: false, AllowedHosts: []string{"127.0.0.1"}}, egress.Store(), nil)
	client := &http.Client{Timeout: 5 * time.Second, Transport: rt}

	// A well-formed token this process's store never minted (wrong key) is unresolvable.
	rogue := "<secret@v1.github-pat.AAAAAAAAAAAAAA.end>"
	_, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader("auth: "+rogue))
	require.Error(t, err)
	require.Contains(t, err.Error(), egress.ErrUnresolvedSentinel.Error(),
		"a token that will not open is refused at the broker, not sent onward")
}

// startEgressEcho runs a loopback http.Server that echoes its request body, so a
// test can assert exactly what the broker put on the wire toward the destination.
func startEgressEcho(t *testing.T) (string, func()) {
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
