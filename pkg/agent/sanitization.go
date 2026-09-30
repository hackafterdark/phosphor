package agent

import (
	"log/slog"

	"github.com/hackafterdark/phosphor/pkg/agent/tools"
)

// This file is the single observable entry point for every content mutation
// the agent applies on purpose: ChatML/control-token defanging, VT100
// device-control stripping, and tool-call JSON trimming. Each helper logs at
// Info level whenever it actually changes the text, so a difference between
// what a tool produced and what the transcript stores is always attributable
// to this pipeline. These mutations are deliberate security and parser
// hardening (see docs/security/SECRETS_PROTECTION.md), never evidence of
// injection or tampering.

// sanitizeToolResultContent applies the tool-result sanitizer pipeline
// (control-token defang then device-control strip) and logs when it mutates
// the content.
func sanitizeToolResultContent(toolName, toolCallID, content string) string {
	defanged := tools.DefangSpecialTokens(content)
	sanitized := tools.StripDeviceControls(defanged)
	if sanitized == content {
		return content
	}
	slog.Info(
		"Tool result sanitized by intentional security pipeline",
		"tool", toolName,
		"tool_call_id", toolCallID,
		"chatml_defanged", defanged != content,
		"device_controls_stripped", sanitized != defanged,
		"original_len", len(content),
		"sanitized_len", len(sanitized),
	)
	return sanitized
}

// defangAssistantText neutralizes inference control tokens in a streamed
// assistant text delta and logs when it mutates the text.
func defangAssistantText(text string) string {
	defanged := tools.DefangSpecialTokens(text)
	if defanged != text {
		slog.Info(
			"Defanged inference control tokens in streamed assistant text",
			"original_len", len(text),
			"defanged_len", len(defanged),
		)
	}
	return defanged
}

// defangReasoningText neutralizes inference control tokens in a streamed
// reasoning/thinking delta and logs when it mutates the text. Reasoning
// content is stored and later replayed back to the model as prior-turn
// context (see Message.ToAIMessage), so a raw token left here reaches the
// model's context on every subsequent turn just as surely as one left in the
// final answer text — it must go through the same defanger as OnTextDelta.
func defangReasoningText(text string) string {
	defanged := tools.DefangSpecialTokens(text)
	if defanged != text {
		slog.Info(
			"Defanged inference control tokens in streamed reasoning text",
			"original_len", len(text),
			"defanged_len", len(defanged),
		)
	}
	return defanged
}

// defangUserPrompt neutralizes inference control tokens in a user prompt
// before it is stored and logs when it mutates the text.
func defangUserPrompt(prompt string) string {
	defanged := tools.DefangSpecialTokens(prompt)
	if defanged != prompt {
		slog.Info(
			"Defanged inference control tokens in user prompt before storing",
			"original_len", len(prompt),
			"defanged_len", len(defanged),
		)
	}
	return defanged
}

// sanitizeToolCallInput trims trailing garbage a vLLM-style tool-call parser
// can append after the JSON body and logs when it mutates the input. The
// trim is a parser workaround, not a content rewrite: the JSON prefix the
// model intended is preserved byte-for-byte.
func sanitizeToolCallInput(toolName, toolCallID, input string) string {
	sanitized := sanitizeJSONInput(input)
	if sanitized == input {
		return input
	}
	slog.Info(
		"Trimmed trailing garbage from tool call JSON input (vLLM parser workaround, intentional)",
		"tool", toolName,
		"tool_call_id", toolCallID,
		"original_len", len(input),
		"sanitized_len", len(sanitized),
	)
	return sanitized
}
