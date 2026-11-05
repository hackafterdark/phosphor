package config

import (
	"fmt"
	"path/filepath"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
)

// ValidateTrustedExtraRoots enforces the path guardrail on
// tools.bash.trusted_extra_roots: every entry must resolve to an absolute
// directory that is neither a filesystem/drive root, a known sensitive
// directory (system directories, the user home, the OS temp dir), an
// ancestor of the workspace, nor in any overlap with the credential
// deny-list. Relative entries are resolved against the working directory
// before the check, and a symlinked entry is checked against its canonical
// target so a link cannot launder a refused root past validation.
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
		abs, err := filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("invalid path %q in tools.bash.trusted_extra_roots: %w", root, err)
		}
		// Lexical check first so drive roots are refused even when they
		// don't exist (or can't be resolved) on this host.
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
