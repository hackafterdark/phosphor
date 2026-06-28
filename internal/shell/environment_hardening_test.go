package shell

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnvironmentHardening_CommandBlockingViaBlockFuncs verifies that
// blockFuncs correctly block banned commands when applied to a shell.
// This tests the core blocking mechanism used by the bash tool.
func TestEnvironmentHardening_CommandBlockingViaBlockFuncs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Test direct command blocking
	directBlockers := []BlockFunc{
		CommandsBlocker([]string{"curl", "wget", "ssh", "sudo"}),
	}

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		BlockFuncs: directBlockers,
	})

	blocked := []string{"curl http://example.com", "wget http://example.com", "ssh user@host", "sudo ls"}
	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			_, _, err := shell.Exec(t.Context(), cmd)
			require.Error(t, err, "command %q should be blocked", cmd)
			require.Contains(t, err.Error(), "not allowed for security reasons",
				"blocked command %q should return security error, got: %v", cmd, err)
		})
	}

	// Test that allowed commands are not blocked
	shell2 := NewShell(&Options{
		WorkingDir: tmpDir,
		BlockFuncs: directBlockers,
	})
	_, _, err := shell2.Exec(t.Context(), "echo hello")
	require.NoError(t, err, "legitimate command should not be blocked")
}

// TestEnvironmentHardening_ArgumentsBlocker verifies that argument-based
// blocking works correctly for package managers and interpreters.
func TestEnvironmentHardening_ArgumentsBlocker(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Build blockFuncs similar to what the bash tool uses
	funcs := []BlockFunc{
		ArgumentsBlocker("npm", []string{"install"}, []string{"-g"}),
		ArgumentsBlocker("pip", []string{"install"}, []string{"--user"}),
		ArgumentsBlocker("python", nil, []string{"-c"}),
		ArgumentsBlocker("node", nil, []string{"-e"}),
	}

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		BlockFuncs: funcs,
	})

	blocked := []string{
		"npm install -g foo",
		"pip install --user foo",
		"python -c 'print(1)'",
		"node -e 'console.log(1)'",
	}

	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			_, _, err := shell.Exec(t.Context(), cmd)
			require.Error(t, err, "command %q should be blocked", cmd)
			require.Contains(t, err.Error(), "not allowed for security reasons",
				"blocked command %q should return security error, got: %v", cmd, err)
		})
	}

	// Test that allowed variants are not blocked
	shell2 := NewShell(&Options{
		WorkingDir: tmpDir,
		BlockFuncs: funcs,
	})
	_, _, err := shell2.Exec(t.Context(), "npm install foo")
	require.NoError(t, err, "npm install without -g should not be blocked")
}

// TestEnvironmentHardening_SelfExecBlocker verifies that the self-execution
// blocker prevents the agent from spawning another Phosphor instance.
func TestEnvironmentHardening_SelfExecBlocker(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		BlockFuncs: []BlockFunc{SelfExecBlocker()},
	})

	blocked := []string{"go run .", "go build .", "phosphor"}
	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			_, _, err := shell.Exec(t.Context(), cmd)
			require.Error(t, err, "command %q should be blocked", cmd)
			require.Contains(t, err.Error(), "not allowed for security reasons",
				"blocked command %q should return security error, got: %v", cmd, err)
		})
	}
}

// TestEnvironmentHardening_SandboxMarkersPresent verifies that PHOSPHOR
// sandbox markers are present in the shell environment when using
// NewShell without explicit Env (default filtering).
func TestEnvironmentHardening_SandboxMarkersPresent(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows; skip sandbox marker test")
	}

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
	})

	markers := []string{"PHOSPHOR=1", "PHOSPHOR_AGENT=true", "AGENT=phosphor", "AI_AGENT=phosphor"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			varName, _, _ := strings.Cut(marker, "=")
			_, out, err := shell.Exec(t.Context(), "bash -c 'echo $"+varName+"'")
			require.NoError(t, err, "command to read %s should succeed", marker)
			require.Equal(t, strings.TrimPrefix(marker, varName+"="), strings.TrimSpace(out),
				"sandbox marker %s should be present in shell environment", marker)
		})
	}
}

// TestEnvironmentHardening_SandboxMarkersWithExplicitEnv verifies that
// sandbox markers are appended even when the caller provides an explicit
// Env slice (bypassing default filtering).
func TestEnvironmentHardening_SandboxMarkersWithExplicitEnv(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows; skip sandbox marker test")
	}

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		Env: []string{"CUSTOM_VAR=hello"},
	})

	markers := []string{"PHOSPHOR=1", "PHOSPHOR_AGENT=true", "AGENT=phosphor", "AI_AGENT=phosphor"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			varName, _, _ := strings.Cut(marker, "=")
			_, out, err := shell.Exec(t.Context(), "bash -c 'echo $"+varName+"'")
			require.NoError(t, err, "command to read %s should succeed", marker)
			require.Equal(t, strings.TrimPrefix(marker, varName+"="), strings.TrimSpace(out),
				"sandbox marker %s should be present even with explicit Env", marker)
		})
	}
}

// TestEnvironmentHardening_FilterEnvWithAllowedEnv verifies that when
// AllowedEnv is configured, only those variables are passed to the shell.
func TestEnvironmentHardening_FilterEnvWithAllowedEnv(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("env filtering behavior varies on Windows; skip for CI stability")
	}

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		AllowedEnv: []string{"PATH", "HOME"},
	})

	// Secret vars should be invisible.
	_, out, err := shell.Exec(t.Context(), "bash -c 'echo ${AWS_SECRET_ACCESS_KEY:-empty}'")
	require.NoError(t, err)
	require.Equal(t, "empty", strings.TrimSpace(out),
		"AWS_SECRET_ACCESS_KEY should not be visible with filtered env")

	// Allowed vars should be visible.
	_, out, err = shell.Exec(t.Context(), "bash -c 'echo ${PATH:-empty}'")
	require.NoError(t, err)
	require.NotEqual(t, "empty", strings.TrimSpace(out),
		"PATH should be visible with AllowedEnv=[PATH,HOME]")
}

// TestEnvironmentHardening_EmptyAllowedEnv_FiltersEverything verifies that
// an empty allowed_env configuration filters ALL environment variables.
func TestEnvironmentHardening_EmptyAllowedEnv_FiltersEverything(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("env filtering behavior varies on Windows; skip for CI stability")
	}

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		AllowedEnv: []string{}, // explicitly empty
	})

	// Even PATH should be invisible with an empty allowlist.
	_, out, err := shell.Exec(t.Context(), "bash -c 'echo ${PATH:-empty}'")
	require.NoError(t, err)
	require.Equal(t, "empty", strings.TrimSpace(out),
		"PATH should not be visible with empty allowed_env")
}
