package memory

// Keywords returns content-word keywords for text. In the default build this is
// the deterministic Tokenize stand-in; in a -tags phosphor_prose build it is the
// gated NLP runtime's multilingual tokenizer with automatic language detection
// (see prose_on.go). It never fails: every fallible path degrades to Tokenize,
// because an entry must not lose its tags to a tokenizer having a bad day.
func Keywords(text string, limit int) []string {
	return proseKeywords(text, limit)
}

// ProseLinked reports whether this binary linked the gated prose runtime
// (MEMORY_SPEC §12 Phase 5). It is false in the default build, where Keywords
// runs the deterministic stand-in.
func ProseLinked() bool { return proseLinked }
