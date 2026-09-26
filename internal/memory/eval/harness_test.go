package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/stretchr/testify/require"
)

// corpusFileID pulls the frontmatter id out of a fixture so the harness can hold the
// seeded set without restating it in Go.
var corpusFileID = regexp.MustCompile(`(?m)^id:\s*(\S+)\s*$`)

// blockAdmittedID pulls the entry id out of one rendered Tier A line. The renderer
// emits "- [type] [pinned] (id) {thread} summary", so the first parenthesised run on
// a bullet is the id and nothing else.
var blockAdmittedID = regexp.MustCompile(`^- [^(]*\(([^)]+)\)`)

// loadManifest reads the curated query set from the package directory, which is the
// working directory of a go test binary.
func loadManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := LoadManifest(".")
	require.NoError(t, err, "the eval gate is only as good as the queries it runs")
	return m
}

// seedCorpus copies the seeded vault into dst/entries and returns the ids it placed,
// so a test can assert on the corpus size rather than trust the file listing.
func seedCorpus(t *testing.T, dst string) []string {
	t.Helper()
	entries := filepath.Join(dst, memory.EntriesDirName)
	require.NoError(t, os.MkdirAll(entries, 0o755))

	raws, err := os.ReadDir(CorpusDir)
	require.NoError(t, err, "the seeded corpus is missing")

	var ids []string
	for _, de := range raws {
		if info, err := de.Info(); err != nil || info.IsDir() {
			continue
		}
		// The manifest lives beside the fixtures, so seeding is only ever the
		// markdown entries; copying the query file into the vault would index the
		// answer key as memory.
		if !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(CorpusDir, de.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(entries, de.Name()), raw, 0o644))

		groups := corpusFileID.FindStringSubmatch(string(raw))
		if len(groups) < 2 {
			t.Fatalf("fixture %s has no frontmatter id", de.Name())
		}
		ids = append(ids, groups[1])
	}
	sort.Strings(ids)
	require.NotEmpty(t, ids, "the seeded corpus is empty")
	return ids
}

// newCorpusStore opens an isolated store over the seeded corpus and returns it
// already indexed, with the lifecycle scores computed.
//
// Every measurement gets its own store on purpose. The vault, the index and the
// adopted window all have to be pristine for a bar to mean anything, and the
// alternative, one shared store that tests mutate, makes one test's write change
// another test's ranking, which is exactly the class of cross-talk this subsystem is
// designed to survive in production and must not reproduce in its own gate.
func newCorpusStore(t *testing.T, over memory.Settings) (*memory.Store, []string) {
	t.Helper()
	dir := t.TempDir()
	ids := seedCorpus(t, dir)

	s, err := memory.Open(memory.OpenOptions{
		GlobalDir: dir,
		Settings:  over,
		Shared:    false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	require.NoError(t, s.Sync(ctx), "the corpus did not index")
	require.NoError(t, s.RefreshLifecycle(ctx), "the lifecycle pass failed on the corpus")

	stats, err := s.Report(ctx)
	require.NoError(t, err)
	require.Equal(t, len(ids), stats.Total, "every fixture has to reach the index")
	require.Equal(t, 0, stats.Quarantined, "no fixture may fail to parse")
	return s, ids
}

// corpusSettings is the shipped tuning with the injected window pinned to a size the
// fixtures can be measured against.
func corpusSettings() memory.Settings {
	return memory.DefaultSettings()
}

// tokenCount is the deterministic token measure. It is the memory package's own
// tokenizer rather than a provider's, which is the only way a token bar can be
// asserted without spending a call: the number is stable across runs and machines,
// and a pass here cannot be bought by a different model.
func tokenCount(text string) int {
	return len(memory.Tokenize(text, 0))
}

// admittedIDs returns the entry ids the rendered window actually carried.
func admittedIDs(t *testing.T, block string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if groups := blockAdmittedID.FindStringSubmatch(line); len(groups) > 1 {
			out = append(out, groups[1])
		}
	}
	return out
}

// containsAll reports whether want is a subset of have.
func containsAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func in(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestEvalCorpusIsSeeded is the precondition gate: if the fixtures do not load and
// index, every bar below would be measured against nothing and pass.
func TestEvalCorpusIsSeeded(t *testing.T) {
	_, ids := newCorpusStore(t, corpusSettings())

	require.Len(t, ids, 21, "the seeded corpus is a fixed set, and a silent change to it moves every bar")

	// The corpus is adversarial on purpose: it contains a retired entry and three
	// pending ones whose vocabulary is deliberately close to the active fixtures, so
	// "retirement holds" and "drafts never surface" are measured against a retrievable
	// near-miss instead of against an absence.
	for _, id := range []string{"mem-lru-cache", "mem-prose-swap", "mem-ragas-open", "mem-sidebar-plan"} {
		require.True(t, in(ids, id), "%s is part of the seeded corpus", id)
	}

	ctx := context.Background()
	s, _ := newCorpusStore(t, corpusSettings())
	for _, id := range []string{"mem-lru-cache", "mem-prose-swap", "mem-ragas-open", "mem-sidebar-plan"} {
		got, err := s.Get(ctx, id)
		require.NoError(t, err)
		require.Empty(t, got, "%s is retired or pending and must not be retrievable at all", id)
	}

	// And the same entries have to be present on disk, otherwise the assertion above
	// would be proving that they were never there rather than that they are excluded.
	entries, err := os.ReadDir(filepath.Join(s.VaultDir(memory.ScopeGlobal), memory.EntriesDirName))
	require.NoError(t, err)
	md := 0
	for _, de := range entries {
		if info, err := de.Info(); err == nil && !info.IsDir() && strings.HasSuffix(de.Name(), ".md") {
			md++
		}
	}
	require.Equal(t, len(ids), md, "the vault is the source of truth, so the files have to be there")
}

// TestEvalReport prints the metric table. It is the artefact a human reads, and it
// is why the bars are named constants rather than inlined into each assertion.
func TestEvalReport(t *testing.T) {
	m := loadManifest(t)
	fmt.Printf("\nMEMORY EVAL GATE (deterministic, no model spent)\n")
	fmt.Printf("  manifest        %d retrieval / %d continuation / %d gate / %d nudge cases\n",
		len(m.Retrieval), len(m.Continuation), len(m.Gate), len(m.Nudge))
	fmt.Printf("  bars            p@%d>=%.2f r@%d>=%.2f continue>=%.2f ask-precision>=%.2f nudge>=%.2f t0<=%d tok churn<=%d\n",
		5, BarPrecisionAt5, 10, BarRecallAt10, BarContinuation, BarGatePrecision, BarNudgeConversion, BarT0Tokens, BarChurn)
	fmt.Printf("  token measure   the memory package's own tokenizer, not a provider's\n\n")
}
