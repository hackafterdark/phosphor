package egress_test

import (
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/stretchr/testify/require"
)

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()
	s, err := egress.NewSecretStore()
	require.NoError(t, err)

	tok, ok := s.Seal("ghp_Sup3rSecretToken12345", "github-pat")
	require.True(t, ok)
	require.True(t, strings.HasPrefix(tok, "<secret@v1.github-pat."), "label preserved in token: %q", tok)
	require.True(t, strings.HasSuffix(tok, ".end>"))
	require.NotContains(t, tok, "ghp_Sup3rSecretToken12345", "plaintext must not appear in the token")

	got, ok := s.Open(tok)
	require.True(t, ok)
	require.Equal(t, "ghp_Sup3rSecretToken12345", got)
}

func TestSealIsStableForSameValue(t *testing.T) {
	t.Parallel()
	s, _ := egress.NewSecretStore()
	a, _ := s.Seal("same-secret-value", "rule")
	b, _ := s.Seal("same-secret-value", "rule")
	require.Equal(t, a, b, "synthetic nonce makes a token stable per (value,label)")

	c, _ := s.Seal("same-secret-value", "other")
	require.NotEqual(t, a, c, "a different label is a different token")
}

func TestOpenRejectsTamperedToken(t *testing.T) {
	t.Parallel()
	s, _ := egress.NewSecretStore()
	tok, _ := s.Seal("super-secret-value", "rule")

	require.True(t, strings.HasSuffix(tok, ".end>"))
	bad := tok[:len(tok)-len(".end>")] + "x.end>"
	_, ok := s.Open(bad)
	require.False(t, ok, "tampering the payload must fail the GCM tag")

	_, ok = s.Open("<secret@v1.rule.AAAA.end>")
	require.False(t, ok, "a forged token must not open")

	_, ok = s.Open("not-a-token")
	require.False(t, ok)
}

func TestSealGuards(t *testing.T) {
	t.Parallel()
	s, _ := egress.NewSecretStore()

	_, ok := s.Seal("tiny", "x") // below minSealLen
	require.False(t, ok)

	big := strings.Repeat("A", 1<<15) // above maxSealLen
	_, ok = s.Seal(big, "x")
	require.False(t, ok)

	s.SetEnabled(false)
	_, ok = s.Seal("a-good-length-secret", "x")
	require.False(t, ok, "disabled store refuses to seal")
	_, ok = s.Open("<secret@v1.x.y.end>")
	require.False(t, ok, "disabled store reports tokens unresolvable")
}

func TestHasToken(t *testing.T) {
	t.Parallel()
	s, _ := egress.NewSecretStore()
	require.False(t, s.HasToken("nothing to see here"))
	tok, _ := s.Seal("a-good-secret-value", "rule")
	require.True(t, s.HasToken("body: "+tok+" trailing"))
}
