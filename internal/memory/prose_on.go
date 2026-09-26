//go:build phosphor_prose

package memory

import (
	"strings"
	"time"
	"unicode"

	prose "github.com/tsawler/prose/v3"
)

// The gated build of the prose seam: link tsawler/prose, the maintained fork of
// the archived jdkato/prose, for the segment+tokenize surface the §13 prune
// permits. NER, POS tagging, and readability stay opted out at the call site
// below, exactly as the prune requires. Languages ship as en/es/fr/de/ja with
// auto-detection, which is what the fork carries over the archived original.
const proseLinked = true

// proseTimeout bounds one keyword extraction. Memory enrichment is best
// effort: a stalled tokenizer must degrade to the stand-in, never stall a write.
const proseTimeout = 2 * time.Second

// trimCutset are the characters a token can collect at its edges that are not
// part of the word underneath them.
const trimCutset = "'\"`.,;:!?·“”‘’«»()[]{}"

func proseKeywords(text string, limit int) []string {
	doc, err := prose.NewMultilingualDocument(text,
		prose.WithExtraction(false),
		prose.WithTagging(false),
		prose.WithTokenization(true),
		prose.WithSegmentation(true),
		prose.WithTimeout(proseTimeout),
	)
	if err != nil {
		return Tokenize(text, limit)
	}
	stop := make(map[string]bool, 64)
	for _, w := range doc.GetStopWords() {
		stop[strings.ToLower(w)] = true
	}
	seen := make(map[string]bool, 32)
	out := make([]string, 0, 8)
	for _, tok := range doc.Tokens() {
		w := strings.ToLower(strings.Trim(tok.Text, trimCutset))
		if len(w) < 3 || seen[w] || stop[w] {
			continue
		}
		if !strings.ContainsFunc(w, unicode.IsLetter) {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return Tokenize(text, limit)
	}
	return out
}
