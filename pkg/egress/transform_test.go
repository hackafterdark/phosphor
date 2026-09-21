package egress_test

import (
	"io"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/stretchr/testify/require"
)

// fakeResolver is a stand-in [egress.TokenResolver] so the transform and transport
// logic can be exercised without real cryptography.
type fakeResolver struct {
	tokens map[string]string
}

func (f fakeResolver) Open(token string) (string, bool) {
	v, ok := f.tokens[token]
	return v, ok
}
func (f fakeResolver) HasToken(text string) bool {
	return strings.Contains(text, "<secret@v1.")
}

func sampleTokens() (map[string]string, string) {
	return map[string]string{
		"<secret@v1.github-pat.AAAA.end>": "ghp_RESOLVED_ONE",
		"<secret@v1.aws-key.BBBB.end>":    "AKIA_RESOLVED_TWO",
	}, ""
}

func TestRestoreStringWholeBuffer(t *testing.T) {
	t.Parallel()
	tokens, _ := sampleTokens()
	r := fakeResolver{tokens}

	in := "auth: <secret@v1.github-pat.AAAA.end> key=<secret@v1.aws-key.BBBB.end> done"
	out, resolved, unresolved := egress.RestoreString(in, r)
	require.Equal(t, 2, resolved)
	require.Equal(t, 0, unresolved)
	require.Equal(t, "auth: ghp_RESOLVED_ONE key=AKIA_RESOLVED_TWO done", out)
}

func TestRestoreStringLeavesUnresolvable(t *testing.T) {
	t.Parallel()
	tokens, _ := sampleTokens()
	r := fakeResolver{tokens}

	in := "here <secret@v1.unknown.CCCC.end> stays"
	out, resolved, unresolved := egress.RestoreString(in, r)
	require.Equal(t, 0, resolved)
	require.Equal(t, 1, unresolved)
	require.Equal(t, in, out, "an unresolvable token is left in place; the caller decides to refuse")
}

// chunkReader hands its bytes out n at a time so a token is guaranteed to be split
// across two Read calls, which is the case the carry-buffer exists for.
type chunkReader struct {
	data []byte
	n    int
	pos  int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	end := c.pos + c.n
	if end > len(c.data) {
		end = len(c.data)
	}
	n := copy(p, c.data[c.pos:end])
	c.pos = end
	return n, nil
}

func TestTransformStreamResolvesSplitToken(t *testing.T) {
	t.Parallel()
	tokens, _ := sampleTokens()
	r := fakeResolver{tokens}

	body := "header <secret@v1.github-pat.AAAA.end>|<secret@v1.aws-key.BBBB.end>|tail"
	want := "header ghp_RESOLVED_ONE|AKIA_RESOLVED_TWO|tail"

	for _, chunk := range []int{1, 2, 3, 7, 13, 64, 4096} {
		up := &chunkReader{data: []byte(body), n: chunk}
		got, err := io.ReadAll(egress.NewTransform(up, r))
		require.NoError(t, err, "chunk size %d", chunk)
		require.Equal(t, want, string(got), "chunk size %d", chunk)
	}
}

func TestTransformPassesThroughPlainText(t *testing.T) {
	t.Parallel()
	r := fakeResolver{map[string]string{}}
	in := "just some ordinary bytes with no tokens at all"
	got, err := io.ReadAll(egress.NewTransform(&chunkReader{data: []byte(in), n: 5}, r))
	require.NoError(t, err)
	require.Equal(t, in, string(got))
}

func TestTransformCarriesIncompleteAndReleasesOnEOF(t *testing.T) {
	t.Parallel()
	r := fakeResolver{map[string]string{}}
	// A token that never closes must be released verbatim at EOF, not dropped.
	in := "leading <secret@v1.github-pat.INCOMPLETE"
	got, err := io.ReadAll(egress.NewTransform(&chunkReader{data: []byte(in), n: 3}, r))
	require.NoError(t, err)
	require.Equal(t, in, string(got))
}

func TestTransformBoundsCarryBuffer(t *testing.T) {
	t.Parallel()
	r := fakeResolver{map[string]string{}}
	// An endless-looking "<secret@v1." with no close must not grow the buffer past
	// the DoS bound; it is eventually flushed as ordinary bytes.
	in := strings.Repeat("<secret@v1.aaaa", 20000)
	got, err := io.ReadAll(egress.NewTransform(&chunkReader{data: []byte(in), n: 4096}, r))
	require.NoError(t, err)
	require.Equal(t, in, string(got))
}
