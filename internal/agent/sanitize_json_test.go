package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeJSONInputValidJSON(t *testing.T) {
	t.Parallel()

	// Valid JSON should pass through unchanged
	input := `{"command": "ls -la"}`
	require.Equal(t, input, sanitizeJSONInput(input))

	input = `{"key": "value", "nested": {"a": 1}}`
	require.Equal(t, input, sanitizeJSONInput(input))

	input = `{}`
	require.Equal(t, input, sanitizeJSONInput(input))

	input = `{"empty": ""}`
	require.Equal(t, input, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputTrailingBrace(t *testing.T) {
	t.Parallel()

	// Stray closing brace should be stripped
	input := `{"command": "ls -la"} }`
	require.Equal(t, `{"command": "ls -la"}`, sanitizeJSONInput(input))

	input = `{"key": "value"} } }`
	require.Equal(t, `{"key": "value"}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputTrailingText(t *testing.T) {
	t.Parallel()

	// Text after closing brace should be stripped
	input := `{"command": "ls -la"} extra text here`
	require.Equal(t, `{"command": "ls -la"}`, sanitizeJSONInput(input))

	input = `{"key": "value"}
more lines`
	require.Equal(t, `{"key": "value"}`, sanitizeJSONInput(input))

	input = `{"command": "echo hello"}
<cwd>/home/user</cwd>`
	require.Equal(t, `{"command": "echo hello"}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputNestedObjects(t *testing.T) {
	t.Parallel()

	// Nested objects should be handled correctly
	input := `{"outer": {"inner": {"deep": true}}} extra`
	require.Equal(t, `{"outer": {"inner": {"deep": true}}}`, sanitizeJSONInput(input))

	input = `{"a": {"b": {"c": {"d": 1}}}}`
	require.Equal(t, input, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputWithStrings(t *testing.T) {
	t.Parallel()

	// Braces inside strings should not be counted
	input := `{"description": "use {curl} command"} extra`
	require.Equal(t, `{"description": "use {curl} command"}`, sanitizeJSONInput(input))

	input = `{"text": "nested { braces } here"} }`
	require.Equal(t, `{"text": "nested { braces } here"}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputWithEscapes(t *testing.T) {
	t.Parallel()

	// Escaped quotes should be handled
	input := `{"command": "echo \"hello\""} extra`
	require.Equal(t, `{"command": "echo \"hello\""}`, sanitizeJSONInput(input))

	input = `{"path": "C:\\Users\\test"} extra`
	require.Equal(t, `{"path": "C:\\Users\\test"}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputArray(t *testing.T) {
	t.Parallel()

	// Arrays should be handled (though less common in tool calls)
	input := `[1, 2, 3] extra`
	require.Equal(t, `[1, 2, 3]`, sanitizeJSONInput(input))

	input = `{"items": [1, 2, 3]} extra`
	require.Equal(t, `{"items": [1, 2, 3]}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputEmpty(t *testing.T) {
	t.Parallel()

	// Empty string passes through unchanged
	require.Equal(t, "", sanitizeJSONInput(""))
	// Whitespace-only strings — no braces found, returns minimal valid JSON
	require.Equal(t, "{}", sanitizeJSONInput("   "))
	require.Equal(t, "{}", sanitizeJSONInput("\n"))
}

func TestSanitizeJSONInputNoClosingBrace(t *testing.T) {
	t.Parallel()

	// No closing brace — return minimal valid JSON so the retry doesn't fail
	input := `{"command": "ls -la"`
	require.Equal(t, "{}", sanitizeJSONInput(input))

	input = `{"incomplete`
	require.Equal(t, "{}", sanitizeJSONInput(input))
}

func TestSanitizeJSONInputMalformedJSON(t *testing.T) {
	t.Parallel()

	// Malformed JSON without proper structure — return minimal valid JSON
	input := `{command: "ls"}`
	require.Equal(t, "{}", sanitizeJSONInput(input))

	input = `{"key": "value"`
	require.Equal(t, "{}", sanitizeJSONInput(input))
}

func TestSanitizeJSONInputMultipleBracesInString(t *testing.T) {
	t.Parallel()

	// Multiple braces inside strings
	input := `{"template": "{{.Name}} {{.Age}}"} extra`
	require.Equal(t, `{"template": "{{.Name}} {{.Age}}"}`, sanitizeJSONInput(input))

	// Simpler backtick test - use string concatenation to avoid backtick-in-backtick
	input = `{"code": "` + "```" + `"}` + " extra"
	require.Equal(t, `{"code": "`+"```"+`"}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputRealToolCall(t *testing.T) {
	t.Parallel()

	// Real-world tool call with trailing content
	input := `{"command": "cd F:/hackafterdark/phosphor && git log --oneline -20 && echo --- && git diff HEAD~1 --stat", "working_dir": "F:/hackafterdark/phosphor", "description": "Check recent commits and diff stat", "run_in_background": false, "auto_background_after": 60}
</cwd>

<cwd>F:/hackafterdark/phosphor</cwd>`
	expected := `{"command": "cd F:/hackafterdark/phosphor && git log --oneline -20 && echo --- && git diff HEAD~1 --stat", "working_dir": "F:/hackafterdark/phosphor", "description": "Check recent commits and diff stat", "run_in_background": false, "auto_background_after": 60}`
	require.Equal(t, expected, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputQwen3StyleOutput(t *testing.T) {
	t.Parallel()

	// Qwen3 models sometimes output extra characters after tool calls
	input := `{"tool": "bash", "arguments": {"command": "ls"}} }`
	require.Equal(t, `{"tool": "bash", "arguments": {"command": "ls"}}`, sanitizeJSONInput(input))

	input = `{"tool": "view", "arguments": {"path": "file.go"}}
</tool_use>`
	require.Equal(t, `{"tool": "view", "arguments": {"path": "file.go"}}`, sanitizeJSONInput(input))
}

func TestSanitizeJSONInputMalformedButBalanced(t *testing.T) {
	t.Parallel()

	// Balanced braces but invalid JSON (unquoted key) — return minimal valid JSON
	input := `{command: "ls"}`
	require.Equal(t, "{}", sanitizeJSONInput(input))

	// Balanced braces but invalid JSON (trailing comma)
	input = `{"key": "value",}`
	require.Equal(t, "{}", sanitizeJSONInput(input))

	// Balanced braces but invalid JSON (single quotes)
	input = `{'key': 'value'}`
	require.Equal(t, "{}", sanitizeJSONInput(input))
}
