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

// inlineExecFlags are the shell flags whose payload argument is an inline
// program rather than an operand file. `-c` (and its bundled variants such
// as `-lc`) executes the next argument; `-s` reads the program from stdin.
// Other leading flags are shell options that may appear before the inline
// flag (`bash --noprofile -c …`); `-C` (noclobber) is deliberately absent —
// it is a plain option that takes no inline program.
var inlineExecFlags = map[string]bool{
	"-c": true,
	"-s": true,
}

// EscapeHatchReason returns a non-empty reason when argv[0] is a host
// escape-hatch binary or an inline shell invocation and therefore must not
// run under the sandbox without an explicit escalation grant. An empty
// return means the command is unconcerned by this gate. kernelBoundary
// reports whether the child exec inherits a kernel confinement boundary:
// while one is attached, `env sh -c …` stays confined through the
// inherited boundary and passes; on degraded hosts the wrapper would
// spawn the nested shell outside any boundary, so it is gated too.
func EscapeHatchReason(argv []string, kernelBoundary bool) string {
	if len(argv) == 0 {
		return ""
	}
	if !kernelBoundary {
		if reason := envNestedShellReason(argv); reason != "" {
			return reason
		}
	}
	name := normalizeBinary(argv[0])
	if hostEscapeBinaries[name] {
		return "host escape-hatch binary " + name + " can leave the sandboxed host security domain"
	}
	if name == "busybox" {
		// BusyBox multiplexes applets: `busybox sh -c '<payload>'` runs the
		// shell applet, so the shell binary moves to argv[1].
		if len(argv) > 1 && inlineShellBinaries[normalizeBinary(argv[1])] {
			if flag, ok := inlineExecFlag(argv[2:]); ok {
				return "inline shell execution via busybox " + argv[1] + " " + flag + " hides its payload from argv-based policy"
			}
		}
		return ""
	}
	if inlineShellBinaries[name] {
		if flag, ok := inlineExecFlag(argv[1:]); ok {
			return "inline shell execution via " + name + " " + flag + " hides its payload from argv-based policy"
		}
	}
	return ""
}

// inlineExecFlag reports the inline-exec flag carried in a shell's option
// prefix. It scans combined flags (`-lc` carries `c`) and plain long
// options (`--noprofile`), stopping at the first operand — once a shell
// reaches its file argument there is no inline program.
func inlineExecFlag(args []string) (string, bool) {
	for _, arg := range args {
		switch {
		case arg == "--":
			return "", false
		case strings.HasPrefix(arg, "--"):
			// Long option; takes no inline program of its own.
			continue
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			for _, opt := range arg[1:] {
				if inlineExecFlags["-"+string(opt)] {
					return "-" + string(opt), true
				}
			}
		default:
			// First operand: the shell runs a script file, not an inline
			// program.
			return "", false
		}
	}
	return "", false
}

// envNestedShellReason detects `env <shell> <inline-flag> …` and
// `env busybox <shell> <inline-flag> …` wrappers. On hosts where kernel
// confinement is unavailable the wrapper runs unconstrained and spawns
// the nested shell outside any inherited boundary, so the inline payload
// reaches the host without ever crossing the escalation grant.
func envNestedShellReason(argv []string) string {
	if normalizeBinary(argv[0]) != "env" || len(argv) < 3 {
		return ""
	}
	rest := argv[1:]
	name := normalizeBinary(rest[0])
	if name == "busybox" && len(rest) > 1 {
		name = normalizeBinary(rest[1])
		rest = rest[1:]
	}
	if !inlineShellBinaries[name] {
		return ""
	}
	if flag, ok := inlineExecFlag(rest[1:]); ok {
		return "env-wrapped inline shell execution via " + name + " " + flag +
			" hides its payload from argv-based policy and escapes the in-process guards"
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
