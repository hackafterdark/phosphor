package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// spillFullOutput writes the complete tool output that would otherwise be
// truncated to `<workspace>/.phosphor/artifacts/<sessionID>/` and returns the
// workspace-relative path plus the sha256 digest of the spilled content.
//
// The file is an append-only side file: the tool result keeps its truncated
// view, and the model can `view` or `grep` the artifact path to recover
// anything the truncation dropped. The digest fingerprints the real content
// so repeated spills of identical output stay detectable.
func spillFullOutput(workspaceDir, sessionID, toolName, content string) (path string, digest string, err error) {
	if workspaceDir == "" || sessionID == "" {
		return "", "", fmt.Errorf("missing workspace or session context")
	}
	dir := filepath.Join(workspaceDir, ".phosphor", "artifacts", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	name := fmt.Sprintf("%s-%d.txt", toolName, time.Now().UnixNano())
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(workspaceDir, full)
	if err != nil {
		_ = os.Remove(full)
		return "", "", err
	}
	sum := sha256.Sum256([]byte(content))
	return filepath.ToSlash(rel), hex.EncodeToString(sum[:]), nil
}

// truncateForSpill bounds content to MaxOutputLength. When truncation occurs
// and session context is available, the full content is spilled to the
// artifact directory and a pointer line is appended to the truncated result.
// Spill failures degrade to plain truncation (same behavior as before).
func truncateForSpill(content, workspaceDir, sessionID, toolName string) string {
	if len(content) <= MaxOutputLength {
		return content
	}
	truncated := TruncateOutput(content)
	relPath, digest, err := spillFullOutput(workspaceDir, sessionID, toolName, content)
	if err != nil {
		slog.Warn("Failed to spill truncated tool output to artifact", "tool", toolName, "error", err)
		return truncated
	}
	return truncated + fmt.Sprintf("\n\n(full output: %s, sha256: %s)", relPath, digest)
}
