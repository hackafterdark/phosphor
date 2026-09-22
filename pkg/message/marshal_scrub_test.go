package message

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hackafterdark/phosphor/pkg/secrets"
)

// TestMarshalParts_AtRestRegistryScrub proves the SQLite copy of an
// assistant-authored tool call and of a tool result is scrubbed of the exact
// values this process registered as credentials, while the caller's in-memory
// parts are left byte-for-byte intact. This is the at-rest backstop for the
// leaked-or-shared session file carrying plaintext the UI never shows gap.
func TestMarshalParts_AtRestRegistryScrub(t *testing.T) {
	t.Parallel()

	// A distinctive value long enough to clear the registry's min-length guard.
	const cred = "sk_livetest_9fj2kqx0abc123"
	secrets.Register(cred)

	cmdInput := "{\"command\":\"echo " + cred + "\"}"
	resContent := "Stripe: " + cred

	parts := []ContentPart{
		ToolCall{ID: "call-1", Name: "bash", Input: cmdInput},
		ToolResult{ToolCallID: "call-1", Name: "bash", Content: resContent},
		TextContent{Text: "unrelated narrative stays"},
	}

	b, err := marshalParts(parts)
	require.NoError(t, err)

	// The credential must not survive into the bytes destined for disk.
	require.NotContains(t, string(b), cred)

	// Surrounding, non-secret content is preserved: the tool name, the literal
	// command text that flanks the scrubbed value, the result label, and the
	// unrelated text part all still marshal out.
	require.Contains(t, string(b), "bash")
	require.Contains(t, string(b), "echo ")
	require.Contains(t, string(b), "Stripe: ")
	require.Contains(t, string(b), "unrelated narrative stays")

	// Critically, the caller's live parts are untouched, so tool execution and
	// the in-memory conversation still carry the original text.
	require.Equal(t, cmdInput, parts[0].(ToolCall).Input)
	require.Equal(t, resContent, parts[1].(ToolResult).Content)
}
