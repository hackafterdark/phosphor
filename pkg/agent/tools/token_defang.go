package tools

import "strings"

// defanger is built once at init time. Order matters: the explicit token
// replacements run first so that e.g. "<|im_end|>" → "[im_end]" before the
// fallback "<|" → "<|​" (ZWS) fires on any remaining unknown sequences.
var defanger = strings.NewReplacer(
	// Hard-replace the most dangerous ChatML / tool-call tokens with bracket
	// notation. Square brackets break the tokenizer's association with the
	// special token IDs (e.g. 151645 for <|im_end|> in Qwen3) so the model
	// reads "[im_end]" rather than the control token, and its probability mass
	// never shifts toward emitting the fatal token when producing its reply.
	"<|im_start|>", "[im_start]",
	"<|im_end|>", "[im_end]",
	"<|call|>", "[call]",
	"<|call_end|>", "[call_end]",
	"<|endoftext|>", "[endoftext]",
	// Fallback: insert a zero-width space after "<|" for any unrecognised
	// sequences so they are still visually readable but parser-inert.
	"<|", "<|​",
)

// DefangSpecialTokens neutralizes inference-engine control tokens in s so they
// cannot poison the LLM context window or trigger vLLM stop sequences.
//
// Known high-risk tokens are replaced with square-bracket notation (e.g.
// "<|im_end|>" → "[im_end]"). Unknown "<|…" sequences get a zero-width space
// as a fallback. Call this on every string that enters the message history from
// outside the agent: file reads, bash output, grep results, MCP results, and
// user prompts.
//
// A pre-pass strips any U+200B zero-width spaces that a previous version of
// this function inserted after "<|". Without this, text like "<​|im_end|>"
// (old ZWS form) would not match the bracket-replacement patterns and would
// reach the model with the ZWS intact — still close enough to the raw token
// that the model's weights reconstruct the real ID in its reply.
func DefangSpecialTokens(s string) string {
	// Fast path: nothing to do.
	if !strings.Contains(s, "<|") && !strings.Contains(s, "​") {
		return s
	}
	// Strip previously-inserted zero-width spaces so "<​|im_end|>" (old ZWS
	// form) collapses back to "<|im_end|>" before the bracket replacer runs.
	if strings.Contains(s, "​") {
		s = strings.ReplaceAll(s, "​", "")
	}
	if !strings.Contains(s, "<|") {
		return s
	}
	return defanger.Replace(s)
}
