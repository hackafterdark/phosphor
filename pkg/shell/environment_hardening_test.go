package shell

import (
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
	// The allowed variant must simply not be refused by the security policy.
	// Asserting absence of the block verdict (rather than a clean exit) keeps
	// this host-independent: where `npm` is installed the real installer may
	// exit non-zero for its own reasons, which is orthogonal to whether the
	// ArgumentsBlocker over-matched a `npm install` invocation lacking `-g`.
	_, _, err := shell2.Exec(t.Context(), "npm install foo")
	if err != nil {
		require.NotContains(t, err.Error(), "not allowed for security reasons",
			"npm install without -g should not be blocked by the security policy")
	}
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

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
	})

	markers := []string{"PHOSPHOR=1", "PHOSPHOR_AGENT=true", "AGENT=phosphor", "AI_AGENT=phosphor"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			varName, _, _ := strings.Cut(marker, "=")
			out, _, err := shell.Exec(t.Context(), "echo \"$"+varName+"\"")
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

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		Env:        []string{"CUSTOM_VAR=hello"},
	})

	markers := []string{"PHOSPHOR=1", "PHOSPHOR_AGENT=true", "AGENT=phosphor", "AI_AGENT=phosphor"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			varName, _, _ := strings.Cut(marker, "=")
			out, _, err := shell.Exec(t.Context(), "echo \"$"+varName+"\"")
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

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		AllowedEnv: []string{"PATH", "HOME"},
	})

	// Secret vars should be invisible.
	out, _, err := shell.Exec(t.Context(), `echo "${AWS_SECRET_ACCESS_KEY:-empty}"`)
	require.NoError(t, err)
	require.Equal(t, "empty", strings.TrimSpace(out),
		"AWS_SECRET_ACCESS_KEY should not be visible with filtered env")

	// Allowed vars should be visible.
	out, _, err = shell.Exec(t.Context(), `echo "${PATH:-empty}"`)
	require.NoError(t, err)
	require.NotEqual(t, "empty", strings.TrimSpace(out),
		"PATH should be visible with AllowedEnv=[PATH,HOME]")
}

// TestEnvironmentHardening_EmptyAllowedEnv_UsesSafeDefaults verifies that an
// empty allowed_env list falls back to the documented safe-default allowlist:
// core variables such as PATH stay visible while arbitrary secrets remain
// filtered out.
func TestEnvironmentHardening_EmptyAllowedEnv_UsesSafeDefaults(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	shell := NewShell(&Options{
		WorkingDir: tmpDir,
		AllowedEnv: []string{}, // empty allowlist → the safe-default allowlist applies
	})

	// An empty allowlist is documented to fall back to the safe defaults, so
	// PATH stays visible while variables outside that set remain hidden.
	out, _, err := shell.Exec(t.Context(), `echo "${PATH:-empty}"`)
	require.NoError(t, err)
	require.NotEqual(t, "empty", strings.TrimSpace(out),
		"PATH should stay visible because an empty allowlist uses the safe defaults")

	out, _, err = shell.Exec(t.Context(), `echo "${AWS_SECRET_ACCESS_KEY:-empty}"`)
	require.NoError(t, err)
	require.Equal(t, "empty", strings.TrimSpace(out),
		"non-allowlisted secret should stay invisible with the default filter")
}
