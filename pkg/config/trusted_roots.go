package config

import (
	"fmt"
	"path/filepath"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
)

// ValidateTrustedExtraRoots enforces the path guardrail on
// tools.bash.trusted_extra_roots: every entry must be an absolute directory
// that is neither a filesystem/drive root, a known sensitive directory
// (system directories, the user home, the OS temp dir), an ancestor of the
// workspace, nor in any overlap with the credential deny-list. The runtime
// consumes the entries verbatim, so relative spellings are refused here
// rather than resolved against whichever directory the process happens to
// run in; a symlinked entry is checked against its canonical target so a
// link cannot launder a refused root past validation.
//
// The check runs at config load on every platform. Landlock fails closed at
// exec time when a deny path sits inside an allowed root
// (Spec.ValidateDenyRead), but the macOS and Windows tiers do not, so
// validating here keeps the policy uniform instead of degrading per-backend.
func (c *Config) ValidateTrustedExtraRoots(workingDir string) error {
	for _, root := range c.Tools.Bash.TrustedExtraRoots {
		if root == "" {
			return fmt.Errorf("empty path in tools.bash.trusted_extra_roots")
		}
		// Lexical pre-check on the raw entry: filepath.Abs is platform
		// semantics, and on POSIX it resolves Windows drive and UNC
		// spellings relative to the working directory instead of
		// recognizing them as volume roots. The refusal is lexical, so
		// the raw spelling is checked before any resolution.
		if sandbox.IsVolumeRootSpelling(root) {
			return fmt.Errorf("path %q in tools.bash.trusted_extra_roots is not allowed: it is a filesystem or drive root and would trust the entire filesystem", root)
		}
		// filepath.Abs resolves relative spellings against the process
		// directory, which is not necessarily the workspace (daemon and
		// ACP hosts differ). The runtime uses entries verbatim, so demand
		// the documented absolute spelling instead of validating a
		// host-dependent resolution.
		if !filepath.IsAbs(root) {
			return fmt.Errorf("path %q in tools.bash.trusted_extra_roots is not allowed: entries must be absolute", root)
		}
		abs := filepath.Clean(root)
		if broad, reason := sandbox.RootTooBroad(abs, workingDir); broad {
			return fmt.Errorf("path %q in tools.bash.trusted_extra_roots is not allowed: %s", root, reason)
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
			if broad, reason := sandbox.RootTooBroad(resolved, workingDir); broad {
				return fmt.Errorf("path %q in tools.bash.trusted_extra_roots is not allowed: resolves to %q, and %s", root, resolved, reason)
			}
		}
	}
	return nil
}
