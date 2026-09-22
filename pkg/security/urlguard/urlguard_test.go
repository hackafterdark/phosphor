package urlguard

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/secrets"
	"github.com/stretchr/testify/require"
)

// knownToken is a deliberately low-entropy, long secret. Its repetition keeps the
// Shannon-entropy scan from firing, so an abort on it can only have come from the
// known-value registry pass. It is registered once for the whole file.
const knownToken = "Zk9aZk9aZk9aZk9aZk9aZk9aZk9a"

func init() {
	secrets.Register(knownToken)
}

func randomHighEntropyToken() string {
	buf := make([]byte, 40)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}
	// URL-safe base64 so the blob survives query parsing without '+' or '=' munging.
	return base64.RawURLEncoding.EncodeToString(buf)
}

func TestCheck_KnownSecretInQueryAborts(t *testing.T) {
	raw := "https://example.com/log?token=" + knownToken
	err := Check(raw)
	require.Error(t, err, "a registered secret smuggled into the query must abort")
	require.Equal(t, ErrMessage, err.Error())
}

func TestCheck_KnownSecretInPathAborts(t *testing.T) {
	// The registry pass runs over the full URL, so a credential in a path segment is
	// caught too, not just the query.
	raw := "https://example.com/" + knownToken + "/report"
	require.ErrorContains(t, Check(raw), ErrMessage)
}

func TestCheck_RegistryIsTheCause_NotEntropy(t *testing.T) {
	// With the speculative entropy pass off, a known secret is still refused, proving the
	// known-value registry is an independent backstop.
	prev := HighEntropyDetection()
	SetHighEntropyDetection(false)
	t.Cleanup(func() { SetHighEntropyDetection(prev) })

	require.Error(t, Check("https://example.com/x?token="+knownToken))
}

func TestCheck_HighEntropyQueryAborts(t *testing.T) {
	prev := HighEntropyDetection()
	SetHighEntropyDetection(true)
	t.Cleanup(func() { SetHighEntropyDetection(prev) })

	// An unregistered but obviously-random token is refused by the entropy scan alone.
	require.ErrorContains(t, Check("https://example.com/dl?sig="+randomHighEntropyToken()), ErrMessage)
}

func TestCheck_HighEntropyDisabledAllowsUnknownToken(t *testing.T) {
	prev := HighEntropyDetection()
	SetHighEntropyDetection(false)
	t.Cleanup(func() { SetHighEntropyDetection(prev) })

	// Same random blob, but with the entropy pass disabled and the value never
	// registered, the guard has nothing to refuse on and lets it through.
	require.NoError(t, Check("https://example.com/dl?sig="+randomHighEntropyToken()))
}

func TestCheck_CleanQueryPasses(t *testing.T) {
	safe := []string{
		"https://example.com/",
		"https://example.com/search?q=hello world&page=2&sort=name_asc",
		"https://example.com/a/b/c?ref=nav.top51271",
		"",
	}
	for _, raw := range safe {
		require.NoError(t, Check(raw), "ordinary query %q must not be blocked", raw)
	}
}

func TestCheck_LongKeyShortValuePasses(t *testing.T) {
	// Only parameter *values* are entropy-scanned, never keys, so a long-looking key
	// with an ordinary value is fine.
	require.NoError(t, Check("https://example.com/x?some_extremely_long_parameter_name_that_looks_random_but_is_not=ok"))
}

func TestScrubURL_RedactsKnownSecret(t *testing.T) {
	raw := "https://example.com/log?token=" + knownToken
	scrubbed := ScrubURL(raw)
	require.NotContains(t, scrubbed, knownToken)
	require.Contains(t, scrubbed, "redacted")
	require.NotEqual(t, raw, ScrubURL("https://example.com/plain?x=1"))
}

// altEncodingSecret contains a character the registry stores only raw and in one
// canonical escape form, so a request can smuggle it past the raw-URL scan using
// a different, equally valid percent-encoding.
const altEncodingSecret = "Ab17-Q.K.PG25!6gkP"

// altEncodedSecretURL spells altEncodingSecret with lower-case hex escapes, which
// matches neither the raw nor the canonical QueryEscape form literally but decodes
// back to the exact registered value.
const altEncodedSecretURL = "https://example.com/log?sig=Ab17-Q.K.PG25%216gkP"

func TestCheck_KnownSecretInAlternateEncodingAborts(t *testing.T) {
	secrets.Register(altEncodingSecret)
	err := Check(altEncodedSecretURL)
	require.Error(t, err, "an alternate-encoded known secret must still abort")
	require.Equal(t, ErrMessage, err.Error())
}

func TestCheck_DecodedRegistryPassRunsWithoutEntropyScan(t *testing.T) {
	secrets.Register(altEncodingSecret)
	prev := HighEntropyDetection()
	SetHighEntropyDetection(false)
	t.Cleanup(func() { SetHighEntropyDetection(prev) })

	// The decoded registry pass is the high-precision control: it stays on even
	// when the speculative entropy scan is dialed back.
	require.ErrorContains(t, Check(altEncodedSecretURL), ErrMessage)
}
