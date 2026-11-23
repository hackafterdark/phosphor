package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultReadWriteDirsExcludeToolchainCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"npm_config_cache", "PIP_CACHE_DIR", "CARGO_HOME", "MAVEN_HOME", "GRADLE_USER_HOME", "UV_CACHE_DIR", "CCACHE_DIR", "BUN_INSTALL", "XDG_DATA_HOME", "NUGET_PACKAGES"} {
		t.Setenv(k, "")
	}

	dirs := DefaultReadWriteDirs()
	// Caches seed every future build; trusting them is an explicit opt-in
	// (tools.bash.trust_toolchain_caches), so the default writable set must
	// never carry them.
	for _, suffix := range []string{".npm", filepath.Join(".cache", "pip"), ".cargo", ".m2", ".gradle", filepath.Join(".cache", "uv"), ".nuget"} {
		require.NotContains(t, dirs, filepath.Join(home, suffix))
	}
}

func TestLanguageCacheDirsAreCacheScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"npm_config_cache", "PIP_CACHE_DIR", "CARGO_HOME", "MAVEN_HOME", "GRADLE_USER_HOME", "UV_CACHE_DIR", "CCACHE_DIR", "BUN_INSTALL", "XDG_DATA_HOME", "XDG_CACHE_HOME", "NUGET_PACKAGES"} {
		t.Setenv(k, "")
	}

	dirs := LanguageCacheDirs()
	require.Contains(t, dirs, filepath.Join(home, ".npm"))
	require.Contains(t, dirs, filepath.Join(home, ".cache", "pip"))
	require.Contains(t, dirs, filepath.Join(home, ".cache", "uv"))
	require.Contains(t, dirs, filepath.Join(home, ".cache", "ccache"))
	require.Contains(t, dirs, filepath.Join(home, ".cargo", "registry"))
	require.Contains(t, dirs, filepath.Join(home, ".cargo", "git"))
	require.Contains(t, dirs, filepath.Join(home, ".m2", "repository"))
	require.Contains(t, dirs, filepath.Join(home, ".gradle", "caches"))
	require.Contains(t, dirs, filepath.Join(home, ".gradle", "wrapper"))
	require.Contains(t, dirs, filepath.Join(home, ".gradle", "native"))
	require.Contains(t, dirs, filepath.Join(home, ".bun", "install", "cache"))
	require.Contains(t, dirs, filepath.Join(home, ".local", "share", "pnpm", "store"))
	require.Contains(t, dirs, filepath.Join(home, ".nuget", "packages"))

	// The parent toolchain homes must stay outside the trusted set: they
	// hold credential and config files (cargo credentials/config.toml,
	// maven settings.xml, gradle.properties) and, for cargo, a bin/ dir on
	// PATH. Trusting the caches must not widen access to any of them.
	require.NotContains(t, dirs, filepath.Join(home, ".cargo"))
	require.NotContains(t, dirs, filepath.Join(home, ".m2"))
	require.NotContains(t, dirs, filepath.Join(home, ".gradle"))
	require.NotContains(t, dirs, filepath.Join(home, ".gradle", "init.d"))
	// The daemon dir carries registry.bin, the host daemon's connection
	// token; granting it would let a confined client commandeer a daemon
	// running outside the sandbox.
	require.NotContains(t, dirs, filepath.Join(home, ".gradle", "daemon"))
	require.NotContains(t, dirs, filepath.Join(home, ".nuget", "NuGet"))
	require.NotContains(t, dirs, filepath.Join(home, ".bun", "bin"))
	require.NotContains(t, dirs, filepath.Join(home, ".local", "share", "pnpm"))
}

func TestLanguageCacheDirsHonorToolchainOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cargo := t.TempDir()
	t.Setenv("CARGO_HOME", cargo)
	t.Setenv("npm_config_cache", "")
	t.Setenv("PIP_CACHE_DIR", "")
	t.Setenv("MAVEN_HOME", "")
	t.Setenv("GRADLE_USER_HOME", "")
	t.Setenv("UV_CACHE_DIR", "")
	t.Setenv("CCACHE_DIR", "")
	t.Setenv("BUN_INSTALL", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("NUGET_PACKAGES", "")

	dirs := LanguageCacheDirs()
	require.Contains(t, dirs, filepath.Join(cargo, "registry"), "CARGO_HOME override must win over the home fallback")
	require.Contains(t, dirs, filepath.Join(cargo, "git"))
	require.NotContains(t, dirs, filepath.Join(home, ".cargo", "registry"))
	// The new ecosystems' override variables resolve the same way the
	// tools themselves resolve them.
	nuget := t.TempDir()
	t.Setenv("NUGET_PACKAGES", nuget)
	require.Contains(t, LanguageCacheDirs(), nuget)
	require.NotContains(t, LanguageCacheDirs(), filepath.Join(home, ".nuget", "packages"))
	// uv resolves its cache UV_CACHE_DIR → XDG_CACHE_HOME/uv → ~/.cache/uv;
	// the XDG step must win before the home fallback.
	xdgCache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	require.Contains(t, LanguageCacheDirs(), filepath.Join(xdgCache, "uv"))
	require.NotContains(t, LanguageCacheDirs(), filepath.Join(home, ".cache", "uv"))
	// UV_CACHE_DIR outranks the XDG directory, exactly as uv itself does.
	uvCache := t.TempDir()
	t.Setenv("UV_CACHE_DIR", uvCache)
	require.Contains(t, LanguageCacheDirs(), uvCache)
	require.NotContains(t, LanguageCacheDirs(), filepath.Join(xdgCache, "uv"))
}

func TestTrustedToolchainCachesIncludesModuleCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	modcache := t.TempDir()
	t.Setenv("GOMODCACHE", modcache)
	for _, k := range []string{"npm_config_cache", "PIP_CACHE_DIR", "CARGO_HOME", "MAVEN_HOME", "GRADLE_USER_HOME", "UV_CACHE_DIR", "CCACHE_DIR", "BUN_INSTALL", "XDG_DATA_HOME", "NUGET_PACKAGES"} {
		t.Setenv(k, "")
	}

	roots := TrustedToolchainCaches()
	// The module cache is a bounded, well-defined path, so it joins the
	// trusted set for dependency-source inspection.
	require.Contains(t, roots, modcache)
	// The language caches are included unchanged.
	require.Contains(t, roots, filepath.Join(home, ".npm"))
	// The standard library is resolved through `go doc`, not by trusting
	// its source directory; GOROOT must not be widened here.
	require.NotContains(t, roots, filepath.Join(home, ".cargo"))
}

func TestTrustedCacheFilesPreCreatesCargoLocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Fresh host with CARGO_HOME present: the mutate lock does not exist
	// until Phosphor creates it, and Landlock needs the file to exist
	// before it can attach an O_PATH rule.
	cargo := t.TempDir()
	t.Setenv("CARGO_HOME", cargo)

	files := TrustedCacheFiles()
	require.Equal(t, []string{
		filepath.Join(cargo, ".package-cache"),
		filepath.Join(cargo, ".package-cache-mutate"),
	}, files)
	for _, f := range files {
		require.FileExists(t, f)
	}
	// The lock files carry no secrets; credentials stay deny-listed.
	spec := SpecFor(t.TempDir(), nil)
	require.Contains(t, spec.DenyRead, filepath.Join(cargo, "credentials.toml"))
}

func TestTrustedCacheFilesSkippedWhenCargoHomeMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Fresh host with no cargo at all: nothing may be created.
	t.Setenv("CARGO_HOME", filepath.Join(home, ".cargo"))

	require.Nil(t, TrustedCacheFiles())
	require.NoDirExists(t, filepath.Join(home, ".cargo"))
}

func TestTrustedCacheFilesRejectSymlinkedCargoLocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// A symlinked lock path would turn the writable file rule into write
	// access to whatever the link resolves to — possibly outside
	// CARGO_HOME — so it must never reach the spec.
	cargo := t.TempDir()
	t.Setenv("CARGO_HOME", cargo)
	target := filepath.Join(home, "host-file")
	require.NoError(t, os.WriteFile(target, []byte("host"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(cargo, ".package-cache")))
	// The second lock is a plain file and must still be granted.
	require.NoError(t, os.WriteFile(filepath.Join(cargo, ".package-cache-mutate"), nil, 0o600))

	files := TrustedCacheFiles()
	require.Equal(t, []string{filepath.Join(cargo, ".package-cache-mutate")}, files)
	// A dangling symlink would be created at its target by O_CREATE; it
	// must be rejected the same way.
	require.NoError(t, os.Remove(filepath.Join(cargo, ".package-cache")))
	require.NoError(t, os.Symlink(filepath.Join(home, "missing"), filepath.Join(cargo, ".package-cache")))
	require.Equal(t, []string{filepath.Join(cargo, ".package-cache-mutate")}, TrustedCacheFiles())
	require.NoFileExists(t, filepath.Join(home, "missing"))
}

func TestForBashMode(t *testing.T) {
	t.Parallel()

	require.Nil(t, ForBashMode("", "/ws"))
	require.Nil(t, ForBashMode("off", "/ws"))
	require.Nil(t, ForBashMode("workspace", ""))

	ws := t.TempDir()
	spec := ForBashModeLimits("workspace", ws, ResourceLimits{})
	require.NotNil(t, spec)
	require.Equal(t, ws, spec.WorkspaceRoot)
	if runtime.GOOS != "windows" {
		// The well-known read-only set is POSIX-shaped and gets filtered
		// to paths that exist, so on Windows the list can legitimately
		// come back empty (the v1 tier cannot kernel-enforce it anyway).
		require.NotEmpty(t, spec.ReadOnlyRoots)
	}
}

func TestResourceLimitsValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, ResourceLimits{}.Validate(), "zero limits mean no limit")
	require.NoError(t, ResourceLimits{MaxProcs: minMaxProcs, MaxFileMB: maxMaxFileMB}.Validate())
	require.NoError(t, ResourceLimits{MaxProcs: maxMaxProcs}.Validate())

	require.Error(t, ResourceLimits{MaxProcs: minMaxProcs - 1}.Validate(),
		"max_procs below the floor must refuse exec (invariant 1)")
	require.Error(t, ResourceLimits{MaxProcs: maxMaxProcs + 1}.Validate())
	require.Error(t, ResourceLimits{MaxFileMB: maxMaxFileMB + 1}.Validate())
}

func TestConfineRejectsInvalidLimits(t *testing.T) {
	t.Parallel()

	// Out-of-range limits refuse the exec in both enforced modes,
	// regardless of host availability — validation happens before the
	// platform isolator is consulted so a misconfig can never leave a
	// half-restricted process.
	cmd := exec.Command("true")
	_, err := Confine(cmd, "workspace", Spec{
		WorkspaceRoot: t.TempDir(),
		Limits:        ResourceLimits{MaxProcs: 1},
	})
	require.Error(t, err)

	_, err = Confine(cmd, "strict", Spec{
		WorkspaceRoot: t.TempDir(),
		Limits:        ResourceLimits{MaxProcs: 1},
	})
	require.Error(t, err)
}

func TestWindowsConfineRejectsLimits(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "windows" {
		t.Skip("windows-only posture check")
	}
	cmd := exec.Command("true")
	cmd.Path = "true.exe"
	// Current() is the windows isolator on this host; rlimits must be
	// refused rather than silently promised.
	err := Current().Confine(cmd, Spec{
		WorkspaceRoot: t.TempDir(),
		Limits:        ResourceLimits{MaxProcs: 128},
	})
	require.Error(t, err, "windows v1 cannot enforce posix rlimits and must refuse rather than promise them")
}

func TestManagedConfineNilSpec(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "strict", nil)
	require.NoError(t, err)
	require.False(t, degraded)
}

func TestManagedConfineOffMode(t *testing.T) {
	t.Parallel()

	spec := ForBashModeLimits("workspace", t.TempDir(), ResourceLimits{})
	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "workspace", spec)
	require.NoError(t, err)
	ok, _ := Available()
	require.Equal(t, !ok, degraded, "workspace mode degrades exactly when the primitives are unavailable")
}

func TestManagedConfineHostBehavior(t *testing.T) {
	t.Parallel()

	spec := ForBashModeLimits("workspace", t.TempDir(), ResourceLimits{})

	// Workspace mode never fails: it either confines or degrades with a
	// visible flag (invariant 1).
	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "workspace", spec)
	require.NoError(t, err)
	if ok, _ := Available(); !ok {
		require.True(t, degraded, "unavailable host must report degraded")
	}

	// Strict mode either confines or refuses exec outright.
	cmd = exec.Command("true")
	degraded, err = ManagedConfine(cmd, "strict", spec)
	if ok, _ := Available(); !ok {
		require.Error(t, err, "strict mode must refuse exec when unavailable")
		var unavailable *SandboxUnavailableError
		require.ErrorAs(t, err, &unavailable)
	} else {
		require.NoError(t, err)
		require.False(t, degraded)
	}
}

func TestCredentialDenyPathsInSpec(t *testing.T) {
	// No t.Parallel: the COMPOSER_HOME override uses t.Setenv.

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	// The default assertions below pin the home-dir layout; neutralize
	// any host override so valid developer configuration cannot fail the
	// test. The explicit override checks further down set these again.
	for _, k := range []string{"CARGO_HOME", "GRADLE_USER_HOME", "XDG_DATA_HOME", "CONAN_HOME", "CONAN_USER_HOME"} {
		t.Setenv(k, "")
	}

	spec := SpecFor(t.TempDir(), nil)
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".ssh"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".aws"))
	// Cargo credentials sit in CARGO_HOME, the parent of the trusted
	// registry/ and git/ cache roots; they must stay denied even though
	// the scoped cache roots already leave them untrusted.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".cargo", "credentials.toml"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".cargo", "credentials"))
	// Podman registry credentials and the Maven/Gradle credential files
	// are the toolchain analogues of the denied registry auth files.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".config", "containers"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".m2", "settings.xml"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".gradle", "gradle.properties"))
	// The Gradle daemon dir holds registry.bin, the host daemon's
	// connection token; it is granted to neither reads nor writes, so a
	// confined client cannot commandeer a daemon outside the sandbox.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".gradle", "daemon"))
	// uv's plaintext credential store lives in the XDG data dir and is
	// never covered by the ~/.cache/uv cache grant; the deny entry is the
	// explicit defense.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".local", "share", "uv", "credentials"))
	// Python/PyPI and Ruby credential stores: same defense-in-depth
	// posture, and the macOS global-read layer's entire defense.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".pypirc"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".gem", "credentials"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".bundle", "config"))
	// Dart/Flutter's pub credential store on the Linux/Windows layout.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".config", "dart", "pub-credentials.json"))
	// C/C++ toolchain debugging: GDB's system-wide init files must be
	// readable, and the user's code-executing gdbinit must stay denied.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".gdbinit"))
	// Composer resolves its home dir (auth.json lives there) by
	// COMPOSER_HOME first; the deny entry must follow the override.
	customComposer := t.TempDir()
	t.Setenv("COMPOSER_HOME", customComposer)
	require.Contains(t, SpecFor(t.TempDir(), nil).DenyRead, filepath.Join(customComposer, "auth.json"))
	// C/C++ toolchain credentials: Conan (v2 and v1 layouts), sccache,
	// and NuGet feed configs.
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".conan2", "credentials.json"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".conan2", "source_credentials.json"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".conan2", ".conan.db"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".conan", ".conan.db"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".conan", "conan.conf"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".nuget", "NuGet", "NuGet.Config"))
	require.Contains(t, spec.DenyRead, filepath.Join(home, ".config", "NuGet", "NuGet.Config"))
	// CONAN_HOME resolves the v2 home; CONAN_USER_HOME is a Conan v1
	// base directory that receives a .conan suffix and must never
	// redirect the v2 home.
	customConan := t.TempDir()
	customConanV1 := t.TempDir()
	t.Setenv("CONAN_HOME", customConan)
	t.Setenv("CONAN_USER_HOME", customConanV1)
	specOverride := SpecFor(t.TempDir(), nil)
	require.Contains(t, specOverride.DenyRead, filepath.Join(customConan, "credentials.json"))
	require.Contains(t, specOverride.DenyRead, filepath.Join(customConan, ".conan.db"))
	require.Contains(t, specOverride.DenyRead, filepath.Join(customConanV1, ".conan", ".conan.db"))
	require.Contains(t, specOverride.DenyRead, filepath.Join(customConanV1, ".conan", "conan.conf"))
	// With CONAN_HOME unset the v2 home stays at ~/.conan2 even when the
	// v1-era variable is set.
	t.Setenv("CONAN_HOME", "")
	require.Contains(t, SpecFor(t.TempDir(), nil).DenyRead,
		filepath.Join(home, ".conan2", "credentials.json"))
	// SCCACHE_CONF is the upstream override variable (not
	// SCCACHE_CONFIG); the deny entry must follow it.
	customSccache := t.TempDir()
	t.Setenv("SCCACHE_CONF", filepath.Join(customSccache, "config"))
	require.Contains(t, SpecFor(t.TempDir(), nil).DenyRead, filepath.Join(customSccache, "config"))
	// The read grant for git/npm registry auth must never land in the
	// deny set, or workspace-confined registry flows would break.
	require.NotContains(t, spec.DenyRead, filepath.Join(home, ".npmrc"))
	// System identity and cluster-credential paths carry the denial to
	// the macOS layer (global reads) and the pathguard; on Linux the
	// Landlock default-deny already covers them.
	require.Contains(t, spec.DenyRead, "/etc/passwd")
	require.Contains(t, spec.DenyRead, "/etc/shadow")
	require.Contains(t, spec.DenyRead, "/run/secrets")
	require.Contains(t, spec.DenyRead, "/var/run/secrets/kubernetes.io/serviceaccount")
}

func TestDefaultReadOnlyDirsExcludeEtc(t *testing.T) {
	t.Parallel()

	// /etc must not be granted as a directory: it holds world-readable
	// sensitive files (/etc/passwd, shadow, host credential material).
	// Only the toolchain-critical per-file rules are granted.
	require.NotContains(t, DefaultReadOnlyDirs, "/etc")
	// GDB's system-wide init is granted as per-file rules, and the
	// pretty-printer scripts live under /usr, which is already a
	// read-only root.
	require.Contains(t, ToolchainEtcFiles(), "/etc/gdb/gdbinit")
	require.Contains(t, ToolchainEtcFiles(), "/etc/gdb/gdbinit.d")
	require.Contains(t, DefaultReadOnlyDirs, "/usr")

	spec := SpecFor(t.TempDir(), nil)
	for _, f := range ToolchainEtcFiles() {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		require.Contains(t, spec.ReadOnlyRoots, f,
			"toolchain-critical /etc file %q must stay readable", f)
	}
}

func TestCredentialDenyPathsHonorCargoHomeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cargo := t.TempDir()
	t.Setenv("CARGO_HOME", cargo)

	spec := SpecFor(t.TempDir(), nil)
	require.Contains(t, spec.DenyRead, filepath.Join(cargo, "credentials.toml"),
		"CARGO_HOME override must win over the home fallback")
}

func TestValidateDenyRead(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()

	// Clean spec: deny path outside every allowed root.
	ok := Spec{
		WorkspaceRoot: ws,
		DenyRead:      []string{filepath.Join(ws, "..", "secrets")},
	}
	require.NoError(t, ok.ValidateDenyRead())

	// Deny path inside the workspace: Landlock cannot carve it out of
	// the writable root, so the spec must fail loudly.
	inside := Spec{
		WorkspaceRoot: ws,
		DenyRead:      []string{filepath.Join(ws, "private")},
	}
	require.Error(t, inside.ValidateDenyRead())

	// Deny path equal to an allowed root is the same conflict.
	equal := Spec{
		WorkspaceRoot: ws,
		ReadOnlyRoots: []string{"/usr"},
		DenyRead:      []string{"/usr"},
	}
	require.Error(t, equal.ValidateDenyRead())

	// Allowed root nested inside a deny path is also unenforceable.
	nested := Spec{
		WorkspaceRoot:  ws,
		ReadWriteRoots: []string{filepath.Join(ws, "cache")},
		DenyRead:       []string{filepath.Dir(ws)},
	}
	require.Error(t, nested.ValidateDenyRead())
}

func TestDefaultSpecPassesDenyReadValidation(t *testing.T) {
	t.Parallel()

	// The built-in credential deny set must never conflict with the
	// default root set on any host we support — if it does, Confine
	// would refuse every exec.
	spec := SpecFor(t.TempDir(), nil)
	require.NoError(t, spec.ValidateDenyRead())
	// With toolchain-cache trust on, the narrowed cache roots join the
	// writable set; none of them may collide with a credential denial.
	caches := SpecFor(t.TempDir(), TrustedToolchainCaches())
	require.NoError(t, caches.ValidateDenyRead())
}

func TestConfinedEnvStripsAgentSocketVars(t *testing.T) {
	t.Parallel()
	env := []string{
		"PATH=/usr/bin",
		"SSH_AUTH_SOCK=/run/user/1000/vscode-ssh-auth-sock-1",
		"SSH_AGENT_PID=4242",
		"HOME=/home/dev",
	}
	got := ConfinedEnv(env)
	require.Equal(t, []string{"PATH=/usr/bin", "HOME=/home/dev"}, got)
	// The input must never be modified in place.
	require.Len(t, env, 4)
}

func TestConfinedEnvPassesThroughBenignVars(t *testing.T) {
	t.Parallel()
	env := []string{"SSH_ASKPASS=/bin/true", "SSH_AUTH_SOCK_PREFIX=x"}
	require.Equal(t, env, ConfinedEnv(env))
}

// envValue returns the value of key in env, or "" when absent.
func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func TestToolchainScratchEnvPinsHostCaches(t *testing.T) {
	// No t.Parallel: t.Setenv mutates the process environment and the
	// scratch GOPATH is a process-wide once-value.
	cache := t.TempDir()
	mod := t.TempDir()
	t.Setenv("GOCACHE", cache)
	t.Setenv("GOMODCACHE", mod)

	env := ToolchainScratchEnv()
	require.NotEmpty(t, env)
	scratch := envValue(env, "GOPATH")
	require.NotEmpty(t, scratch)
	require.DirExists(t, scratch)
	// The host caches are pinned explicitly; without them Go would
	// re-derive both from the scratch GOPATH and duplicate GBs into /tmp.
	require.Equal(t, mod, envValue(env, "GOMODCACHE"))
	require.Equal(t, cache, envValue(env, "GOCACHE"))
	// The scratch GOPATH lives under the OS temp dir, which
	// DefaultReadWriteDirs already makes writable for confined children.
	rel, err := filepath.Rel(os.TempDir(), scratch)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."))
}

func TestToolchainScratchEnvSkippedWithoutHostModCache(t *testing.T) {
	// If no host module cache resolves, pinning GOPATH to the scratch dir
	// would let Go create <scratch>/pkg/mod and duplicate the cache into
	// /tmp — the injection must be skipped entirely.
	if runtime.GOOS == "windows" {
		t.Skip("HOME cannot be unset portably on Windows")
	}
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPATH", "")
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Unset HOME so goModCacheDir cannot fall back to home/go/pkg/mod.
	require.NoError(t, os.Unsetenv("HOME"))
	t.Cleanup(func() { _ = os.Setenv("HOME", home) })

	require.Nil(t, ToolchainScratchEnv())
}

func TestCleanupToolchainScratchIsNoOpBeforeCreation(t *testing.T) {
	// Cleanup must never trigger lazy creation: a run that never spawned
	// a confined shell must not create (and delete) a temp dir.
	if toolchainScratchDir != "" {
		t.Skip("scratch dir already created by another test in this run")
	}
	CleanupToolchainScratch()
	require.Empty(t, toolchainScratchDir)
}

func TestCleanupToolchainScratchRemovesCreatedDir(t *testing.T) {
	created := toolchainScratchGopath()
	require.NotEmpty(t, created)
	t.Cleanup(func() { CleanupToolchainScratch() })

	CleanupToolchainScratch()
	_, err := os.Stat(created)
	require.True(t, os.IsNotExist(err))
	require.Empty(t, toolchainScratchDir)
	// After cleanup the next confined shell gets a fresh unique dir.
	fresh := toolchainScratchGopath()
	require.NotEmpty(t, fresh)
	require.NotEqual(t, created, fresh)
}
