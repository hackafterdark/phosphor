package sandbox

import (
	"strings"
)

// hostEscapeBinaries are host binaries the agent's own exec path can use to
// leave the host security domain. "wsl" hands execution to the WSL2 VM
// kernel, which host-side Landlock/seccomp cannot reach; cmd and the
// PowerShell family are host shells whose payloads the in-process argv
// guards never parse. Kernel confinement still covers their children
// (inherited token / wrapper), but the policy posture is deny-by-default:
// these binaries require an explicit escalation grant while the sandbox is
// on.
var hostEscapeBinaries = map[string]bool{
	"wsl":        true,
	"wslx":       true,
	"cmd":        true,
	"powershell": true,
	"pwsh":       true,
}

// inlineShellBinaries are POSIX shells that, invoked with an inline-exec
// flag, carry the dangerous payload inside a quoted argument — invisible
// to argv-based deny rules and, on degraded hosts, to the in-process
// guards as well. Gating the inline-exec forms keeps `sh script.sh` (which
// the script dispatcher confines by path) working while forcing an
// escalation grant for `sh -c '<payload>'`.
var inlineShellBinaries = map[string]bool{
	"sh":      true,
	"bash":    true,
	"zsh":     true,
	"dash":    true,
	"ksh":     true,
	"ash":     true,
	"busybox": true,
}

// inlineExecFlags are the shell flags whose remaining arguments are an
// inline program rather than operands.
var inlineExecFlags = map[string]bool{
	"-c": true,
	"-C": true,
	"-s": true,
}

// EscapeHatchReason returns a non-empty reason when argv[0] is a host
// escape-hatch binary or an inline shell invocation and therefore must not
// run under the sandbox without an explicit escalation grant. An empty
// return means the command is unconcerned by this gate.
func EscapeHatchReason(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	name := normalizeBinary(argv[0])
	if hostEscapeBinaries[name] {
		return "host escape-hatch binary " + name + " can leave the sandboxed host security domain"
	}
	if inlineShellBinaries[name] && len(argv) > 1 && inlineExecFlags[argv[1]] {
		return "inline shell execution via " + name + " " + argv[1] + " hides its payload from argv-based policy"
	}
	return ""
}

// normalizeBinary reduces argv[0] to a lowercase base name without the
// Windows .exe suffix, so path-prefixed and case-variant spellings
// (C:\Windows\System32\cmd.exe, C:/WINDOWS/system32/WindowsPowerShell/v1.0/powershell.EXE)
// all match the same deny entries. Splits on both separators on every
// platform — the sandbox may gate Windows-style paths from a cross-compiled
// or WSL-adjacent context, and filepath.Base is OS-dependent.
func normalizeBinary(argv0 string) string {
	base := strings.ToLower(argv0)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".exe")
}
