package agent

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Control-token material is built from numeric runes so no raw control-token
// bigram ever appears in this file's source bytes, and raw vs bracket forms
// stay distinguishable at runtime even when display channels render both the
// same way (they proved indistinguishable through the view tool this session,
// which silently corrupted earlier assertions here).

func specialToken(name string) string {
	return string([]rune{0x3c, 0x7c}) + name + string([]rune{0x7c, 0x3e})
}

// bracketToken builds the inert bracket-notation form the defanger produces.
func bracketToken(name string) string {
	return string(rune(0x5b)) + name + string(rune(0x5d))
}

// esc is the ESC byte used to build device-control sequences.
const esc = string(rune(0x1b))

// nl is a newline for content that must avoid literal escapes in source.
const nl = string(rune(0x0a))

// captureLogs swaps the default logger for a buffer-backed one and returns
// the buffer plus a restore function.
func captureLogs(t *testing.T) (*syncBuffer, func()) {
	t.Helper()
	var mu sync.Mutex
	buf := &syncBuffer{mu: &mu}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	return buf, func() {
		slog.SetDefault(prev)
	}
}

type syncBuffer struct {
	mu *sync.Mutex
	b  []byte
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.b)
}

func TestSanitizeToolResultContent_CleanPassesThroughWithoutLog(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	content := "plain tool output with {json} and braces"
	got := sanitizeToolResultContent("bash", "call-1", content)
	require.Equal(t, content, got)
	require.Empty(t, buf.String())
}

func TestSanitizeToolResultContent_DefangIsLogged(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	raw := specialToken("im_end")
	in := "see " + raw + " here"
	got := sanitizeToolResultContent("view", "call-2", in)
	require.NotEqual(t, in, got)
	require.Contains(t, got, bracketToken("im_end"))
	require.NotContains(t, got, raw)

	logs := buf.String()
	require.Contains(t, logs, "Tool result sanitized by intentional security pipeline")
	require.Contains(t, logs, "chatml_defanged=true")
	require.Contains(t, logs, "device_controls_stripped=false")
	require.Contains(t, logs, "view")
}

func TestSanitizeToolResultContent_DeviceControlStripIsLogged(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	in := "log line" + nl + esc + "]52;0;c2VjcmV0cGF5bG9hZA==" + string(rune(0x07)) + nl + "next"
	got := sanitizeToolResultContent("bash", "call-3", in)
	require.NotContains(t, got, esc+"]")

	logs := buf.String()
	require.Contains(t, logs, "Tool result sanitized by intentional security pipeline")
	require.Contains(t, logs, "chatml_defanged=false")
	require.Contains(t, logs, "device_controls_stripped=true")
}

func TestDefangAssistantText_LogsOnlyWhenMutated(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	clean := defangAssistantText("no tokens here")
	require.Equal(t, "no tokens here", clean)
	require.Empty(t, buf.String())

	mutated := defangAssistantText("ends with " + specialToken("im_start"))
	require.Contains(t, mutated, bracketToken("im_start"))
	require.Contains(t, buf.String(), "Defanged inference control tokens in streamed assistant text")
}

func TestDefangReasoningText_LogsOnlyWhenMutated(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	clean := defangReasoningText("plain chain-of-thought")
	require.Equal(t, "plain chain-of-thought", clean)
	require.Empty(t, buf.String())

	mutated := defangReasoningText("thinking about " + specialToken("im_end") + " here")
	require.Contains(t, mutated, bracketToken("im_end"))
	require.NotContains(t, mutated, specialToken("im_end"))
	require.Contains(t, buf.String(), "Defanged inference control tokens in streamed reasoning text")
}

func TestDefangUserPrompt_LogsOnlyWhenMutated(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	clean := defangUserPrompt("hello agent")
	require.Equal(t, "hello agent", clean)
	require.Empty(t, buf.String())

	mutated := defangUserPrompt("x " + specialToken("endoftext") + " y")
	require.Contains(t, mutated, bracketToken("endoftext"))
	require.Contains(t, buf.String(), "Defanged inference control tokens in user prompt before storing")
}

func TestDefangUserPrompt_UnknownTokenInerted(t *testing.T) {
	raw := specialToken("unknown_tok")
	got := defangUserPrompt("a " + raw + " b")
	require.NotEqual(t, "a "+raw+" b", got)
	require.NotContains(t, got, raw)
}

func TestSanitizeToolCallInput_LogsTrim(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	clean := `{"command": "ls"}`
	require.Equal(t, clean, sanitizeToolCallInput("bash", "call-4", clean))
	require.Empty(t, buf.String())

	garbage := clean + " }"
	got := sanitizeToolCallInput("bash", "call-5", garbage)
	require.Equal(t, clean, got)

	logs := buf.String()
	require.Contains(t, logs, "Trimmed trailing garbage from tool call JSON input")
	require.Contains(t, logs, "tool=bash")
	require.Contains(t, logs, "tool_call_id=call-5")
}
