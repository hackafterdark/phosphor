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

	"github.com/hackafterdark/phosphor/internal/filepathext"
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
	// sessionID reaches the filesystem path directly, and sub-agent
	// session IDs embed a model-supplied tool-call ID: it must stay a
	// single path component or `../` can redirect writes outside the
	// artifact directory.
	if sessionID != filepath.Base(sessionID) || strings.ContainsAny(sessionID, `/\`) {
		return "", "", fmt.Errorf("invalid session id %q for artifact spill", sessionID)
	}
	// codeql[go/path-injection] sessionID is validated above to a single
	// path component; every derived path is additionally verified against
	// the workspace after creation below, fail-closed.
	dir := filepath.Join(workspaceDir, ".phosphor", "artifacts", sessionID)
	// A committed symlink at `.phosphor` or `artifacts` would redirect
	// the spill outside the workspace: check the resolved directory
	// stays inside the workspace before creating or writing anything.
	// Failures degrade to plain truncation via the caller.
	absWorkspace, err := filepath.Abs(workspaceDir)
	if err != nil {
		return "", "", err
	}
	if !filepathext.IsInside(dir, absWorkspace) {
		return "", "", fmt.Errorf("artifact dir %q resolves outside workspace %q", dir, absWorkspace)
	}
	// codeql[go/path-injection] dir passed the containment check above;
	// the realpath re-check after creation refuses an ancestor symlink
	// that appeared between the check and the mkdir (TOCTOU).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	// The pre-creation check resolves symlinks once; a concurrently
	// sandboxed child could swap an ancestor for a symlink to outside the
	// workspace before the write. Re-check the created directory and fail
	// closed so a redirected spill never persists.
	if !filepathext.IsInside(dir, absWorkspace) {
		return "", "", fmt.Errorf("artifact dir %q resolves outside workspace %q after creation", dir, absWorkspace)
	}
	name := fmt.Sprintf("%s-%d.txt", toolName, time.Now().UnixNano())
	full := filepath.Join(dir, name)
	// codeql[go/path-injection] full stays under the dir re-verified above;
	// the file name is internal tool constants plus a nanosecond timestamp.
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		return "", "", err
	}
	// Fail closed on a post-write swap too: remove the file if it ended up
	// resolving outside the workspace.
	if !filepathext.IsInside(full, absWorkspace) {
		_ = os.Remove(full)
		return "", "", fmt.Errorf("artifact file %q resolves outside workspace %q after write", full, absWorkspace)
	}
	// codeql[go/path-injection] full is workspace-contained per the
	// post-write re-check, so the relative path cannot escape the
	// workspace.
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
