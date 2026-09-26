//go:build !phosphor_prose

package memory

// The default build of the prose seam: the gated NLP runtime is not linked.
// MEMORY_SPEC §13 pruned prose to segment+tokenize on size grounds, and the
// tsawler/prose fork measures +7.3 MB linked (measured 2026-09-23), so the
// default binary must not pay for it. The deterministic Tokenize stand-in is
// the keyword path here, which is the §13 decision verbatim.
//
// A -tags phosphor_prose build swaps in prose_on.go, which keeps the same two
// symbols and changes only their bodies.
const proseLinked = false

func proseKeywords(text string, limit int) []string {
	return Tokenize(text, limit)
}
