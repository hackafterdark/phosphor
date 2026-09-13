package phosphordocs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/phosphordocs"
	"github.com/stretchr/testify/require"
)

func TestListAndRead(t *testing.T) {
	docs := phosphordocs.List()
	require.NotEmpty(t, docs, "expected a non-empty embedded corpus")

	// A doc we know ships must be listed and readable.
	var found bool
	for _, d := range docs {
		if d.RelPath == "commands/GOAL.md" {
			found = true
			require.NotEmpty(t, d.Title)
			require.Equal(t, "phosphor://docs/commands/GOAL.md", d.VirtualPath)
		}
	}
	require.True(t, found, "commands/GOAL.md should be in the corpus")

	data, ok := phosphordocs.ReadFile("commands/GOAL.md")
	require.True(t, ok)
	require.Contains(t, string(data), "goal")

	// Bare and virtual forms resolve identically.
	data2, ok2 := phosphordocs.ReadFile("phosphor://docs/commands/GOAL.md")
	require.True(t, ok2)
	require.Equal(t, data, data2)

	// A missing doc reports not-found rather than panicking.
	_, ok = phosphordocs.ReadFile("does/not/exist.md")
	require.False(t, ok)
}

func TestExcludedNotShipped(t *testing.T) {
	for _, rel := range []string{
		"adr/ADRs.md",
		"architecture/OVERVIEW.md",
		"hooks/FUTURE.md",
		"security/SECURITY_VALIDATION_TEST_SUITE.md",
	} {
		require.False(t, phosphordocs.Exists(rel), "%s must not be shipped", rel)
	}
}

func TestIsIncluded(t *testing.T) {
	require.True(t, phosphordocs.IsIncluded("commands/GOAL.md"))
	require.True(t, phosphordocs.IsIncluded("SYSTEM_PROMPT.md"))
	require.False(t, phosphordocs.IsIncluded("adr/0001-x.md"))
	require.False(t, phosphordocs.IsIncluded("architecture/OVERVIEW.md"))
	require.False(t, phosphordocs.IsIncluded("hooks/FUTURE.md"))
	require.False(t, phosphordocs.IsIncluded("hooks/examples/rtk-rewrite.sh"))
	require.False(t, phosphordocs.IsIncluded("README.md")) // reserved landing doc
}

func TestSearch(t *testing.T) {
	results, err := phosphordocs.Search(context.Background(), "goal objective", 5)
	require.NoError(t, err)
	require.NotEmpty(t, results, "searching for 'goal' should hit the goals doc")

	var hit bool
	for _, r := range results {
		if strings.Contains(strings.ToLower(r.RelPath), "goal") {
			hit = true
		}
	}
	require.True(t, hit, "the goals doc should rank for a goal query")

	// A single opaque token that matches no corpus term returns nothing rather than
	// erroring. (Multi-word queries are OR'd, so stray common words would match.)
	none, err := phosphordocs.Search(context.Background(), "zzqxvqtkelvinplanktop", 5)
	require.NoError(t, err)
	require.Empty(t, none)
}

// TestDriftGuard is the single-source-of-truth guarantee: every doc the allow-list
// says to ship must be embedded byte-for-byte identical to the canonical docs tree.
// If someone edits docs/ without running the generator, this fails.
func TestDriftGuard(t *testing.T) {
	repoRoot := mustRepoRoot(t)
	src := filepath.Join(repoRoot, "docs")

	var checked int
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !phosphordocs.IsIncluded(rel) {
			return nil
		}
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, ok := phosphordocs.ReadFile(rel)
		require.Truef(t, ok, "%s is allow-listed but missing from the corpus; run go generate ./pkg/phosphordocs", rel)
		require.Equalf(t, []byte(want), got, "%s drifted from the canonical docs; run go generate ./pkg/phosphordocs", rel)
		checked++
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, checked, 10, "expected to actually verify a meaningful number of docs")
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqualf(t, parent, dir, "go.mod not found above test working directory")
		dir = parent
	}
}
