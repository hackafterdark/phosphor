package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
//
// Containment is enforced by the filesystem, not by a check: the workspace is
// opened once as an os.Root and every directory/file operation is relative to
// it, so a sandboxed child swapping in a symlink to outside the workspace
// cannot redirect the spill (os.Root rejects symlink components that leave
// the root).
func spillFullOutput(workspaceDir, sessionID, toolName, content string) (path string, digest string, err error) {
	if workspaceDir == "" || sessionID == "" {
		return "", "", fmt.Errorf("missing workspace or session context")
	}
	// sessionID reaches the filesystem path directly, and sub-agent
	// session IDs embed a model-supplied tool-call ID: it must stay a
	// single path component or `../` can redirect writes outside the
	// artifact directory.
	if sessionID != filepath.Base(sessionID) || strings.ContainsAny(sessionID, `/\`) {
		return "", "", fmt.Errorf("invalid session id %q for artifact spill", sessionID)
	}
	root, err := os.OpenRoot(workspaceDir)
	if err != nil {
		return "", "", err
	}
	defer root.Close()

	// sessionID is validated above to a single path component, and the
	// os.Root operations cannot resolve outside the workspace root even if
	// an ancestor becomes a symlink concurrently.
	dir := filepath.Join(".phosphor", "artifacts", sessionID)
	// codeql[go/path-injection]
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	name := fmt.Sprintf("%s-%d.txt", toolName, time.Now().UnixNano())
	full := filepath.Join(dir, name)
	// full stays root-relative: the file name is internal tool constants
	// plus a nanosecond timestamp, and the root rejects any resolution
	// outside the workspace.
	// codeql[go/path-injection]
	f, err := root.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		// Cleanup of the file this call just created inside the root; see
		// the containment note above.

		// codeql[go/path-injection]
		_ = root.Remove(full)
		return "", "", err
	}
	if err := f.Close(); err != nil {
		// codeql[go/path-injection] same cleanup path as above.
		_ = root.Remove(full)
		return "", "", err
	}
	sum := sha256.Sum256([]byte(content))
	return filepath.ToSlash(full), hex.EncodeToString(sum[:]), nil
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
