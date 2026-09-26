package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeVocabFixture(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(
		"module github.com/hackafterdark/phosphor\n\n"+
			"go 1.27.0\n\n"+
			"require charm.land/fantasy v0.45.0\n\n"+
			"require (\n\tgithub.com/tsawler/prose/v3 v3.0.0-beta2 // indirect\n\tmodernc.org/sqlite v1.59.0\n)\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Taskfile"), []byte(
		"version: '3.44.0'\n\ntasks:\n  build:\n    desc: compile the binary\n    cmds:\n      - go build ./...\n  lint:fix:\n    cmds:\n      - golangci-lint run .\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(
		"# Build Test Lint Commands\n\n- go test ./...\n"), 0o644))
}

func TestProjectVocabularyCollectsProjectTerms(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeVocabFixture(t, dir)

	terms := ProjectVocabulary(dir)
	for _, want := range []string{"phosphor", "fantasy", "prose", "sqlite", "build", "lint:fix", "internal", "pkg"} {
		require.Contains(t, terms, want)
	}
	// Noise the sources contain but the vocabulary must not: structural YAML keys,
	// major-version segments, build junk, and dot-directories.
	for _, unwanted := range []string{"version", "vars", "tasks", "cmds", "v3", "beta2", "node_modules", ".git"} {
		require.NotContains(t, terms, unwanted)
	}
}

func TestSeedVocabularyIsIdempotentAndScopedToTheProjectBank(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ws := t.TempDir()
	writeVocabFixture(t, ws)
	s := openTestStore(t, DefaultSettings())

	first, err := s.SeedVocabulary(ctx, ws)
	require.NoError(t, err)
	require.Greater(t, first, 3)

	second, err := s.SeedVocabulary(ctx, ws)
	require.NoError(t, err)
	require.Equal(t, first, second)

	var count int
	err = s.DB(ScopeProject).QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tags WHERE kind = 'vocab' AND term = 'fantasy'").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// The global vault stays clean: one workspace's vocabulary is not a universal.
	var global int
	err = s.DB(ScopeGlobal).QueryRowContext(ctx, "SELECT COUNT(*) FROM tags").Scan(&global)
	require.NoError(t, err)
	require.Equal(t, 0, global)
}

func TestSeedVocabularyEmptyWorkspaceIsANoOp(t *testing.T) {
	t.Parallel()
	s := openTestStore(t, DefaultSettings())
	n, err := s.SeedVocabulary(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.Equal(t, 0, n)
	n, err = s.SeedVocabulary(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
