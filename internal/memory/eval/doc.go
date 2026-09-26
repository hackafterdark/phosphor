// Package eval is the deterministic gate over the memory subsystem described in
// MEMORY_SPEC.md section 11. It is the empirical answer to the two questions the
// design cannot settle in prose: does retrieval return the right things, and does
// the agent actually reach for the writer.
//
// It is a gate, not a scoreboard. Every measurement here runs against the real
// store, the real FTS5 index and the real write gate, and none of it spends a
// model call or a token: the corpus is seeded from files on disk, the ranking is
// BM25, the lifecycle is SQL, and the token counts are the package's own
// deterministic tokenizer rather than a provider's. That is deliberate. A gate that
// needs a model cannot run in CI, cannot be trusted to be reproducible, and would
// make the feature's cost depend on the very thing the feature exists to reduce.
//
// The corpus lives in corpus/ and is shared with any future model-scored
// benchmark, which is a separate layer over the same fixtures and is deliberately
// not part of this package: a judge is opt-in, a gate is not.
//
// Run it with:
//
//	go test ./internal/memory/eval -run Eval
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The bars are section 11's v1 pass thresholds, named here so the assertions and
// the report cannot drift apart from each other.
const (
	// BarPrecisionAt5 is the fraction of the first five hits that must be relevant.
	BarPrecisionAt5 = 0.8
	// BarRecallAt10 is the fraction of the relevant set that must appear in the
	// first ten hits.
	BarRecallAt10 = 0.9
	// BarContinuation is how reliably a keyword-less continuation prompt has to be
	// resolvable to the right thread by the names-only hint alone.
	BarContinuation = 0.85
	// BarGatePrecision is the fraction of surfaced asks a reader would call
	// warranted. An ask that is not warranted is the interrupt the design promises
	// not to generate.
	BarGatePrecision = 0.8
	// BarNudgeConversion is the fraction of eligible decision turns that end in a
	// write. It is the feature's primary health metric.
	BarNudgeConversion = 0.7
	// BarT0Tokens is the standing per-session cost ceiling of the always-injected
	// block, measured in the memory package's own token stream.
	BarT0Tokens = 400
	// BarChurn is how much the injected window may change between renders of an
	// unchanged vault. Zero: the block is the thing the provider prefix cache is
	// priced against.
	BarChurn = 0
	// BarNonPrimaryRefusal is the fraction of non-primary writes that must be
	// refused. Anything below total is a contamination hole.
	BarNonPrimaryRefusal = 1.0
)

// ManifestFile is the curated query set the gate is driven by.
const ManifestFile = "corpus/queries.json"

// CorpusDir is the seeded vault the fixtures are copied from.
const CorpusDir = "corpus"

// Manifest is the whole curated query set.
type Manifest struct {
	Retrieval    []RetrievalCase    `json:"retrieval"`
	Continuation []ContinuationCase `json:"continuation"`
	Gate         []GateCase         `json:"gate"`
	Nudge        []NudgeCase        `json:"nudge"`
}

// RetrievalCase is one query over the seeded corpus.
type RetrievalCase struct {
	ID    string `json:"id"`
	Query string `json:"query"`
	// Thread and Tags are optional filters, mirroring the memory_search schema.
	Thread string   `json:"thread,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Types  []string `json:"types,omitempty"`
	// ExpectIDs is the relevant set. Precision is scored against
	// min(k, len(ExpectIDs)) rather than k, because a curated golden set names the
	// answers that exist and an eight-item relevant set would otherwise make a
	// perfect first five look like a mediocre one.
	ExpectIDs []string `json:"expect_ids"`
	// MustNot is a hard veto, scored independent of the bars.
	MustNot []string `json:"must_not,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// ContinuationCase is a keyword-less prompt that only the thread hint can resolve.
type ContinuationCase struct {
	ID            string   `json:"id"`
	Prompt        string   `json:"prompt"`
	ExpectThreads []string `json:"expect_threads"`
	Note          string   `json:"note,omitempty"`
}

// GateCase is one write against the real gate with the decision a reader would have
// called correct.
type GateCase struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Asserted  bool   `json:"asserted"`
	Pinned    bool   `json:"pinned"`
	ExpectAsk bool   `json:"expect_ask"`
	Note      string `json:"note,omitempty"`
}

// NudgeCase is one completed turn against the post-turn reminder.
type NudgeCase struct {
	ID          string `json:"id"`
	TurnText    string `json:"turn_text"`
	Primary     *bool  `json:"primary,omitempty"`
	WroteMemory bool   `json:"wrote_memory,omitempty"`
	ExpectNudge bool   `json:"expect_nudge"`
	Note        string `json:"note,omitempty"`
}

// IsPrimary resolves the case's agent context, defaulting to the primary one the
// way the real post-turn seam does.
func (c NudgeCase) IsPrimary() bool {
	return c.Primary == nil || *c.Primary
}

// LoadManifest reads and validates the curated query set. A malformed or empty
// manifest is a hard failure rather than a silently skipped suite: an eval that
// measures nothing has to be loud about it.
func LoadManifest(root string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if err != nil {
		return m, fmt.Errorf("read query manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if len(m.Retrieval) == 0 {
		return m, fmt.Errorf("%s declares no retrieval cases", ManifestFile)
	}
	if len(m.Gate) == 0 {
		return m, fmt.Errorf("%s declares no gate cases", ManifestFile)
	}
	if len(m.Nudge) == 0 {
		return m, fmt.Errorf("%s declares no nudge cases", ManifestFile)
	}
	for _, c := range m.Retrieval {
		if c.Query == "" {
			return m, fmt.Errorf("retrieval case %q has no query", c.ID)
		}
	}
	return m, nil
}
