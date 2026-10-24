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
//
// The language-runtime caches (npm, pip, cargo, maven, gradle) are NOT
// included: a cache directory is trusted input for every future build, so
// widening the writable set to it is an explicit operator decision
// (tools.bash.trust_toolchain_caches). When trusted, the narrowed cache
// roots join the writable set via ExtraTrustedRoots, keeping the kernel
// spec and the in-process pathguard on one list.
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

// TrustedToolchainCaches returns the toolchain cache roots the agent is
// allowed to read and write when tools.bash.trust_toolchain_caches is on:
// the narrowed language-runtime caches (LanguageCacheDirs) plus the active
// GOMODCACHE so dependency source can be inspected.
//
// The Go standard library is deliberately absent: its source lives under
// GOROOT, which is already on the kernel read-only roots, and `go doc`
// resolves stdlib and module packages through the toolchain itself — no
// pathguard widening required. Enumerating stdlib source directories
// per-language would leave the two enforcement layers unable to stay in
// lockstep, so stdlib reads route through `go doc` instead.
func TrustedToolchainCaches() []string {
	roots := LanguageCacheDirs()
	if mod := goModCacheDir(); mod != "" {
		roots = append(roots, mod)
	}
	return roots
}

// LanguageCacheDirs returns the cache roots for the common language
// toolchains, honoring each tool's canonical override variable
// (npm_config_cache, PIP_CACHE_DIR, CARGO_HOME, MAVEN_HOME,
// GRADLE_USER_HOME) the same way the tools themselves do. Home-relative
// fallbacks follow each tool's default.
//
// The roots are deliberately scoped to cache content only: cargo gets
// registry/ and git/ but not ~/.cargo itself (excluding the bin/ dir that
// sits on PATH, the credentials file, and config.toml, which can redirect
// the registry), maven gets repository/ but not settings.xml, and gradle
// gets caches/ and wrapper/ but not gradle.properties. Credential files
// under the excluded parents stay neither writable nor readable in the
// sandbox, and the same list feeds the in-process pathguard so both layers
// agree on what is trusted.
func LanguageCacheDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	cargoHome := firstNonEmpty(os.Getenv("CARGO_HOME"), filepath.Join(home, ".cargo"))
	mavenHome := firstNonEmpty(os.Getenv("MAVEN_HOME"), filepath.Join(home, ".m2"))
	gradleHome := firstNonEmpty(os.Getenv("GRADLE_USER_HOME"), filepath.Join(home, ".gradle"))
	return []string{
		firstNonEmpty(os.Getenv("npm_config_cache"), filepath.Join(home, ".npm")),
		firstNonEmpty(os.Getenv("PIP_CACHE_DIR"), filepath.Join(home, ".cache", "pip")),
		filepath.Join(cargoHome, "registry"),
		filepath.Join(cargoHome, "git"),
		filepath.Join(mavenHome, "repository"),
		filepath.Join(gradleHome, "caches"),
		filepath.Join(gradleHome, "wrapper"),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// SpecFor builds the workspace-mode sandbox policy. The workspace root is
// always writable; ExtraTrustedRoots (pathguard's trusted additions) join
// the writable set; well-known read-only roots are filtered to paths that
// actually exist on this host.
func SpecFor(workspace string, extraTrustedRoots []string) Spec {
	return SpecForLimits(workspace, extraTrustedRoots, ResourceLimits{})
}

// SpecForLimits is SpecFor with explicit resource caps applied to the
// confined child.
func SpecForLimits(workspace string, extraTrustedRoots []string, limits ResourceLimits) Spec {
	return Spec{
		WorkspaceRoot:  workspace,
		ReadOnlyRoots:  existing(DefaultReadOnlyDirs),
		ReadWriteRoots: append(append([]string{}, DefaultReadWriteDirs()...), extraTrustedRoots...),
		ReadOnlyFiles:  userReadOnlyFiles(),
		ReadWriteFiles: []string{"/dev/null"},
		Limits:         limits,
	}
}

// ForBashMode builds the sandbox policy for a managed child (bash tool,
// MCP stdio server) when kernel sandboxing is requested. Returns nil when
// sandboxing is off, invalid, or no workspace is configured.
func ForBashMode(mode, workspace string) *Spec {
	if !ModeEnabled(mode) || workspace == "" {
		return nil
	}
	spec := SpecFor(workspace, nil)
	return &spec
}

// ForBashModeLimits is ForBashMode with explicit resource caps.
func ForBashModeLimits(mode, workspace string, limits ResourceLimits) *Spec {
	if !ModeEnabled(mode) || workspace == "" {
		return nil
	}
	spec := SpecForLimits(workspace, nil, limits)
	return &spec
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
		if err := spec.Limits.Validate(); err != nil {
			return false, err
		}
		iso := Current()
		if ok, _ := iso.Available(); !ok {
			if mode == "strict" {
				return false, &SandboxUnavailableError{Reason: "platform sandbox unavailable"}
			}
			// Degraded workspace mode still gets whatever partial
			// kernel containment the platform can apply (Windows
			// restricted token); the caller surfaces the warning.
			_ = iso.Confine(cmd, spec)
			return true, nil
		}
		if err := iso.Confine(cmd, spec); err != nil {
			if mode == "strict" {
				return false, err
			}
			// Degrade per-tier: drop the network posture the kernel
			// cannot enforce and retry with the FS boundary only, so
			// workspace mode keeps the strongest enforcement the host
			// supports instead of dropping to in-process guards.
			relaxed := spec
			relaxed.Network = NetworkAllow
			relaxed.ProxyPort = 0
			if err2 := iso.Confine(cmd, relaxed); err2 != nil {
				return true, nil
			}
			return true, nil
		}
		return false, nil
	default:
		return false, nil
	}
}

// ManagedConfine confines a long-lived managed child (MCP stdio server)
// with a policy built once at startup. strict mode returns the error so
// the caller refuses the connection; workspace mode confines with the
// strongest posture the host supports and reports degraded=true when the
// policy could not attach fully. A nil spec leaves the command untouched.
func ManagedConfine(cmd *exec.Cmd, mode string, spec *Spec) (degraded bool, err error) {
	if spec == nil {
		return false, nil
	}
	return Confine(cmd, mode, *spec)
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
