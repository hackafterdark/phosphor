// Package externalcontent implements Phase 10 of the Secret Protection Plan:
// prompt-injection hardening for untrusted external content.
//
// External content — web pages, email bodies, webhook payloads, MCP tool
// results, and browser tool output — can contain LLM chat-template tokens,
// Unicode homoglyphs, and instruction-injection payloads that hijack the
// model's behaviour.  This package provides two layers of defence:
//
//  1. [Sanitize]: strips / brackets all known chat-template control tokens
//     across major open-weights model families, and folds Unicode homoglyphs
//     so that fullwidth or zero-width variants of dangerous sequences are also
//     caught.
//
//  2. [Wrap]: calls Sanitize and then encloses the result in HTML-comment
//     boundary markers with a cryptographically random per-call ID.  The
//     random ID prevents an attacker from crafting an "END" marker in advance
//     to escape the untrusted block.
//
// # Relationship to token_defang.go
//
// pkg/agent/tools.DefangSpecialTokens targets ChatML specifically and acts as
// a backstop on every string that enters the agent's stored message history.
// This package is a broader, more aggressive sanitizer meant to run on
// content before it reaches the agent pipeline at all: it covers every
// major model family, folds Unicode homoglyphs, and wraps content in boundary
// markers so the model knows not to trust embedded instructions.
//
// # Usage
//
//	// Wrap a web-fetch result before inserting into the agent's context:
//	safe := externalcontent.Wrap(fetchedBody, "web-fetch")
//
//	// Sanitize without boundary markers (e.g. when the caller provides its
//	// own structural framing):
//	clean := externalcontent.Sanitize(rawMCPResult)
package externalcontent

// Sanitize neutralizes LLM chat-template control tokens and Unicode homoglyphs
// in content from an untrusted external source.
//
// Two passes:
//  1. [foldHomoglyphs]: drops zero-width/invisible chars, maps fullwidth and
//     lookalike Unicode code points to their ASCII equivalents.
//  2. tokenReplacer: replaces every known chat-template control token (ChatML,
//     Llama 2/3, Gemma, DeepSeek, Phi, GPT FIM…) with safe bracket notation
//     that breaks the tokenizer's association with special token IDs.
//
// Sanitize is idempotent: calling it twice returns the same result as once.
func Sanitize(content string) string {
	if content == "" {
		return content
	}
	content = foldHomoglyphs(content)
	content = tokenReplacer.Replace(content)
	return content
}
