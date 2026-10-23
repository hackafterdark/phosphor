package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultReadOnlyDirs are the well-known roots a development toolchain must
// read and execute inside the sandbox. Missing paths are dropped silently
// (they differ per distro/platform); the spec stays honest either way.
var DefaultReadOnlyDirs = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/etc",
	"/dev",
	"/proc",
	"/sys",
	"/run",
	"/opt",
	"/nix",
	"/home/linuxbrew/.linuxbrew",
	"/Applications",
	"/Library",
	"/System",
	"/usr/local",
	"/private",
	"/Volumes",
}

// userReadOnlyFiles returns single-file exceptions tools must read. git
// treats EACCES on its config files as fatal (unlike ENOENT), so these
// need file-level rules, not dir rules.
func userReadOnlyFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range []string{".gitconfig", ".gitignore_global", ".hgrc", ".npmrc"} {
		p := filepath.Join(home, f)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// DefaultReadWriteDirs are the writable locations a confined dev toolchain
// needs: the OS temp dir, and the active GOCACHE/GOMODCACHE when they live
// outside the workspace. GOMODCACHE is writable so toolchain downloads
// (GOTOOLCHAIN) don't wedge; a stricter profile can drop it later.
func DefaultReadWriteDirs() []string {
	var out []string
	if tmp := os.TempDir(); tmp != "" {
		out = append(out, tmp)
	}
	if cache := goCacheDir(); cache != "" {
		out = append(out, cache)
	}
	if mod := goModCacheDir(); mod != "" {
		out = append(out, mod)
	}
	return out
}

// SpecFor builds the workspace-mode sandbox policy. The workspace root is
// always writable; ExtraTrustedRoots (pathguard's trusted additions) join
// the writable set; well-known read-only roots are filtered to paths that
// actually exist on this host.
func SpecFor(workspace string, extraTrustedRoots []string) Spec {
	return Spec{
		WorkspaceRoot:  workspace,
		ReadOnlyRoots:  existing(DefaultReadOnlyDirs),
		ReadWriteRoots: append(append([]string{}, DefaultReadWriteDirs()...), extraTrustedRoots...),
		ReadOnlyFiles:  userReadOnlyFiles(),
		ReadWriteFiles: []string{"/dev/null"},
	}
}

func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func goCacheDir() string {
	if v := os.Getenv("GOCACHE"); v != "" {
		return v
	}
	if cache := cacheDir(); cache != "" {
		return filepath.Join(cache, "go-build")
	}
	return ""
}

func goModCacheDir() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return v
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "pkg", "mod")
	}
	return ""
}

func cacheDir() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache")
	}
	return ""
}

// Confine wraps cmd's argv through the platform isolator if the sandbox is
// active for this spec. mode=off leaves cmd untouched; workspace degrades
// to no-sandbox with degraded=true (caller warns); strict returns the
// error.
func Confine(cmd *exec.Cmd, mode string, spec Spec) (degraded bool, err error) {
	switch strings.ToLower(mode) {
	case "", "off":
		return false, nil
	case "workspace", "strict":
		iso := Current()
		if ok, _ := iso.Available(); !ok {
			if mode == "strict" {
				return false, &SandboxUnavailableError{Reason: "platform sandbox unavailable"}
			}
			return true, nil
		}
		if err := iso.Confine(cmd, spec); err != nil {
			if mode == "strict" {
				return false, err
			}
			return true, nil
		}
		return false, nil
	default:
		return false, nil
	}
}

// SandboxUnavailableError is returned in strict mode when enforcement
// cannot attach. Callers must treat it as a fatal exec refusal.
type SandboxUnavailableError struct{ Reason string }

func (e *SandboxUnavailableError) Error() string {
	return "sandbox enforcement unavailable: " + e.Reason
}

// Available reports platform sandbox availability for phosphor_info.
func Available() (ok bool, detail string) {
	return Current().Available()
}

// ModeEnabled reports whether sandboxing is requested for this mode string.
func ModeEnabled(mode string) bool {
	return strings.EqualFold(mode, "workspace") || strings.EqualFold(mode, "strict")
}
