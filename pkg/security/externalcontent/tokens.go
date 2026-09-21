package externalcontent

import "strings"

// tokenReplacer maps known chat-template control tokens from all major
// open-weights model families to safe bracket notation. Built once at init.
//
// strings.NewReplacer uses a multi-pattern matcher (Aho-Corasick variant):
// the first-defined pattern wins when multiple patterns would match at the
// same position.  Longer / more-specific patterns must therefore be listed
// before any shorter prefix they share — e.g. "<|im_end|>" before the
// catch-all "<|" fallback at the bottom.
//
// This replacer operates on text that has already been processed by
// foldHomoglyphs, so fullwidth / homoglyph variants have already been
// collapsed to ASCII before any match is attempted here.
var tokenReplacer = strings.NewReplacer(
	// ---- ChatML / Qwen / Yi -----------------------------------------------
	// Token IDs in Qwen3: im_start=151644, im_end=151645. vLLM terminates the
	// stream the instant token 151645 is emitted anywhere — even mid-thought.
	"<|im_start|>", "[im_start]",
	"<|im_end|>", "[im_end]",
	"<|call|>", "[call]",
	"<|call_end|>", "[call_end]",
	"<|endoftext|>", "[endoftext]",

	// ---- Llama 2 / Mistral v1-v2 ------------------------------------------
	// These are text markers, not special token IDs, but they act as role
	// delimiters and can cause instruction injection if echoed literally.
	"[INST]", "[inst]",
	"[/INST]", "[/inst]",
	"<<SYS>>", "[sys]",
	"<</SYS>>", "[/sys]",

	// ---- Llama 3 ----------------------------------------------------------
	"<|begin_of_text|>", "[begin_of_text]",
	"<|end_of_text|>", "[end_of_text]",
	"<|start_header_id|>", "[start_header_id]",
	"<|end_header_id|>", "[end_header_id]",
	"<|eot_id|>", "[eot_id]",
	"<|python_tag|>", "[python_tag]",

	// ---- Gemma / Gemma 2 --------------------------------------------------
	"<start_of_turn>", "[start_of_turn]",
	"<end_of_turn>", "[end_of_turn]",

	// ---- DeepSeek (after homoglyph normalization) -------------------------
	// Raw tokens use fullwidth | (U+FF5C) and ▁ (U+2581); foldHomoglyphs
	// maps those to | and _ respectively, leaving these ASCII forms to match.
	"<|begin_of_sentence|>", "[begin_of_sentence]",
	"<|end_of_sentence|>", "[end_of_sentence]",
	"<|User|>", "[User]",
	"<|Assistant|>", "[Assistant]",
	"<|System|>", "[System]",
	"<|DeepThink|>", "[DeepThink]",
	"<|tool▁calls▁begin|>", "[tool_calls_begin]",
	"<|tool▁call▁begin|>", "[tool_call_begin]",
	"<|tool▁call▁end|>", "[tool_call_end]",
	"<|tool▁calls▁end|>", "[tool_calls_end]",
	"<|tool_calls_begin|>", "[tool_calls_begin]",
	"<|tool_call_begin|>", "[tool_call_begin]",
	"<|tool_call_end|>", "[tool_call_end]",
	"<|tool_calls_end|>", "[tool_calls_end]",

	// ---- Phi-3 / Phi-4 ----------------------------------------------------
	"<|user|>", "[user]",
	"<|assistant|>", "[assistant]",
	"<|system|>", "[system]",
	"<|end|>", "[end]",

	// ---- GPT / OpenAI FIM tokens (appear in open-weights distils) ---------
	"<|fim_prefix|>", "[fim_prefix]",
	"<|fim_suffix|>", "[fim_suffix]",
	"<|fim_middle|>", "[fim_middle]",
	"<|file_separator|>", "[file_separator]",

	// ---- Catch-all: any remaining unrecognised <|…> bigram ---------------
	// This fires on any <| that survived the explicit replacements above.
	// Inserting a ZWS breaks the tokenizer's bigram association without
	// destroying readability; the model is instructed to treat external
	// content <| sequences as inert anyway.
	"<|", "<|​",
)
