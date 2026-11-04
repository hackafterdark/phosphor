package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpillFullOutput_WritesArtifactAndDigest(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	content := strings.Repeat("output line\n", 100)

	path, digest, err := spillFullOutput(ws, "session-123", "bash", content)
	require.NoError(t, err)

	sum := sha256.Sum256([]byte(content))
	require.Equal(t, hex.EncodeToString(sum[:]), digest)

	// Path is workspace-relative and inside the artifacts dir.
	require.True(t, filepath.IsLocal(filepath.FromSlash(path)), "path must be workspace-relative: %s", path)
	require.True(t, strings.HasPrefix(path, ".phosphor/artifacts/session-123/"), "unexpected path: %s", path)

	data, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(path)))
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

func TestSpillFullOutput_MissingContext(t *testing.T) {
	t.Parallel()

	_, _, err := spillFullOutput("", "session", "bash", "x")
	require.Error(t, err)

	_, _, err = spillFullOutput(t.TempDir(), "", "bash", "x")
	require.Error(t, err)
}

func TestTruncateForSpill_ShortContentUnchanged(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	content := "short output"

	got := truncateForSpill(content, ws, "session-1", "bash")
	require.Equal(t, content, got)

	// No artifact directory is created for content within the limit.
	_, err := os.Stat(filepath.Join(ws, ".phosphor", "artifacts"))
	require.True(t, os.IsNotExist(err))
}

func TestTruncateForSpill_LongContentSpillsPointer(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	content := strings.Repeat("l", MaxOutputLength*2)

	got := truncateForSpill(content, ws, "session-2", "bash")
	require.Contains(t, got, "lines truncated")
	require.Contains(t, got, "(full output: .phosphor/artifacts/session-2/bash-")
	require.Contains(t, got, "sha256: ")

	// The displayed output is bounded; the spilled file keeps everything.
	sum := sha256.Sum256([]byte(content))
	wantDigest := hex.EncodeToString(sum[:])
	require.Contains(t, got, wantDigest)

	entries, err := os.ReadDir(filepath.Join(ws, ".phosphor", "artifacts", "session-2"))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(ws, ".phosphor", "artifacts", "session-2", entries[0].Name()))
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

func TestTruncateForSpill_MissingContextDegradesToTruncation(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("l", MaxOutputLength*2)

	got := truncateForSpill(content, "", "", "bash")
	require.Equal(t, TruncateOutput(content), got)
	require.NotContains(t, got, "full output:")
}
