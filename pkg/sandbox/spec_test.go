package sandbox

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultReadWriteDirsExcludeToolchainCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"npm_config_cache", "PIP_CACHE_DIR", "CARGO_HOME", "MAVEN_HOME", "GRADLE_USER_HOME"} {
		t.Setenv(k, "")
	}

	dirs := DefaultReadWriteDirs()
	// Caches seed every future build; trusting them is an explicit opt-in
	// (tools.bash.trust_toolchain_caches), so the default writable set must
	// never carry them.
	for _, suffix := range []string{".npm", filepath.Join(".cache", "pip"), ".cargo", ".m2", ".gradle"} {
		require.NotContains(t, dirs, filepath.Join(home, suffix))
	}
}

func TestLanguageCacheDirsAreCacheScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"npm_config_cache", "PIP_CACHE_DIR", "CARGO_HOME", "MAVEN_HOME", "GRADLE_USER_HOME"} {
		t.Setenv(k, "")
	}

	dirs := LanguageCacheDirs()
	require.Contains(t, dirs, filepath.Join(home, ".npm"))
	require.Contains(t, dirs, filepath.Join(home, ".cache", "pip"))
	require.Contains(t, dirs, filepath.Join(home, ".cargo", "registry"))
	require.Contains(t, dirs, filepath.Join(home, ".cargo", "git"))
	require.Contains(t, dirs, filepath.Join(home, ".m2", "repository"))
	require.Contains(t, dirs, filepath.Join(home, ".gradle", "caches"))
	require.Contains(t, dirs, filepath.Join(home, ".gradle", "wrapper"))

	// The parent toolchain homes must stay outside the trusted set: they
	// hold credential and config files (cargo credentials/config.toml,
	// maven settings.xml, gradle.properties) and, for cargo, a bin/ dir on
	// PATH. Trusting the caches must not widen access to any of them.
	require.NotContains(t, dirs, filepath.Join(home, ".cargo"))
	require.NotContains(t, dirs, filepath.Join(home, ".m2"))
	require.NotContains(t, dirs, filepath.Join(home, ".gradle"))
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

	dirs := LanguageCacheDirs()
	require.Contains(t, dirs, filepath.Join(cargo, "registry"), "CARGO_HOME override must win over the home fallback")
	require.Contains(t, dirs, filepath.Join(cargo, "git"))
	require.NotContains(t, dirs, filepath.Join(home, ".cargo", "registry"))
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
	require.NotEmpty(t, spec.ReadOnlyRoots)
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
