package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// sensitiveRoots are well-known directories that tools.bash.trusted_extra_roots
// must never grant: they either expose the whole filesystem or contain the
// credential material the sandbox deny-list is built to protect. The list
// covers the POSIX and Windows layouts; current-volume roots (C:\, D:\, the
// drive holding the working directory) are checked separately in
// [RootTooBroad]. Legitimate sub-directories of these roots (for example
// C:\Tools\llvm\bin or /usr/local/mytool) stay listable — only the sensitive
// root itself is refused.
var sensitiveRoots = []string{
	// POSIX system directories.
	"/etc",
	"/root",
	"/boot",
	"/proc",
	"/sys",
	"/dev",
	"/run",
	"/var",
	"/tmp",
	// Windows system directories (checked against the %*DIR% values and the
	// literal spelling, whichever the host resolves).
	`C:\Windows`,
	`C:\Program Files`,
	`C:\Program Files (x86)`,
	`C:\ProgramData`,
	`C:\Users`,
}

// driveRootRe matches a Windows drive-letter root spelling ("C:\", "D:/")
// on any host. filepath.VolumeName only recognizes drive roots on Windows,
// so the lexical check needs its own pattern: refusing the spelling on
// every platform is correct even when the volume is unknown here, because
// the same config may run on a host where the volume exists.
var driveRootRe = regexp.MustCompile(`^[A-Za-z]:[\\/]?$`)

// sensitiveEnvDirs are the environment variables whose values name
// sensitive roots on this host, matched case-insensitively at the platform's
// filesystem comparison semantics.
func sensitiveEnvDirs() []string {
	var out []string
	for _, name := range []string{"WINDIR", "SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "CommonProgramFiles"} {
		if v := os.Getenv(name); v != "" {
			out = append(out, filepath.Clean(v))
		}
	}
	return out
}

// RootTooBroad reports whether dir names a directory that
// tools.bash.trusted_extra_roots must never trust, and says why. It is the
// shared guardrail for every sandbox backend: the kernel tiers cannot all
// enforce the credential deny-list inside a broad allow root (Landlock is
// allow-list-only; the Windows tiers cannot deny reads in-kernel), so the
// policy is enforced once, loudly, at config load on every platform instead
// of degrading per-backend at exec time.
//
// workspaceRoot may be empty when the caller has no workspace context; the
// workspace-ancestor rule is skipped in that case.
func RootTooBroad(dir, workspaceRoot string) (bool, string) {
	home, homeErr := os.UserHomeDir()
	temp := filepath.Clean(os.TempDir())
	ws := filepath.Clean(workspaceRoot)

	return RootTooBroadFor(dir, home, homeErr, temp, ws)
}

// RootTooBroadFor is RootTooBroad with injectable home/temp values so tests
// can pin the behavior on hosts whose real home and temp dirs differ.
func RootTooBroadFor(dir, home string, homeErr error, temp, workspaceRoot string) (bool, string) {
	if dir == "" {
		return false, ""
	}
	clean := filepath.Clean(normalizeRoot(dir))

	if isVolumeRoot(clean) {
		return true, "it is a filesystem or drive root and would trust the entire filesystem"
	}
	// macOS (and any host whose sensitive directories sit behind symlink
	// aliases, e.g. /etc -> /private/etc) can name a sensitive root through
	// its canonical target; check the resolved form of the candidate too.
	forms := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
		forms = append(forms, filepath.Clean(resolved))
	}
	for _, form := range forms {
		for _, sensitive := range sensitiveRoots {
			if samePath(form, sensitive) {
				return true, fmt.Sprintf("it is the well-known system directory %q", form)
			}
		}
		for _, sensitive := range sensitiveEnvDirs() {
			if samePath(form, sensitive) {
				return true, fmt.Sprintf("it is the host system directory %q", form)
			}
		}
		if homeErr == nil && home != "" && samePath(form, filepath.Clean(home)) {
			return true, "it is the user home directory, which contains the credential deny-list (SSH keys, cloud tokens, git credentials)"
		}
		if temp != "" && samePath(form, temp) {
			return true, "it is the OS temporary directory, which is already trusted by default"
		}
		if workspaceRoot != "" {
			if samePath(form, workspaceRoot) {
				return true, "it is the workspace itself, which is already trusted by default"
			}
			if insidePath(workspaceRoot, form) {
				return true, fmt.Sprintf("it is an ancestor of the workspace (%q), which disables the workspace confinement", workspaceRoot)
			}
		}
		for _, deny := range CredentialDenyPaths() {
			deny = filepath.Clean(deny)
			if insidePath(deny, form) {
				return true, fmt.Sprintf("it contains credential deny-list path %q, which this backend cannot enforce inside an allowed root", deny)
			}
			if insidePath(form, deny) {
				return true, fmt.Sprintf("it is inside credential deny-list path %q, which is unreadable in the sandbox", deny)
			}
		}
	}
	return false, ""
}

// normalizeRoot strips the Windows extended-length path prefix ("\\?\")
// so the UNC spelling of a sensitive root matches its normal form
// ("\\?\C:\Windows" -> "C:\Windows"). The regular path is returned
// unchanged for everything else, including UNC server shares, which are
// handled by the volume-root check instead.
func normalizeRoot(dir string) string {
	if !strings.HasPrefix(dir, `\\?\`) {
		return dir
	}
	rest := dir[len(`\\?\`):]
	if strings.HasPrefix(rest, `UNC\`) {
		return `\\` + rest[len(`UNC\`):]
	}
	return rest
}

// isVolumeRoot reports whether cleanPath is a filesystem root ("/", "C:\",
// "C:/", or a UNC share root). The input is expected to be filepath.Clean'd.
// Drive-letter roots are recognized on every platform, not just Windows:
// filepath.VolumeName only parses them on Windows hosts, and a refused
// spelling must fail on all of them.
func isVolumeRoot(cleanPath string) bool {
	if cleanPath == "" {
		return false
	}
	if driveRootRe.MatchString(cleanPath) {
		return true
	}
	if vol := filepath.VolumeName(cleanPath); vol != "" {
		rest := cleanPath[len(vol):]
		return rest == "" || rest == `\` || rest == "/"
	}
	return cleanPath == "/" || cleanPath == `\`
}

// samePath compares two cleaned paths, case-insensitively on Windows where
// the filesystem itself is case-insensitive.
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// insidePath reports whether inner is lexically inside outer (equal paths
// return false; use samePath for equality).
func insidePath(inner, outer string) bool {
	if outer == "" {
		return false
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(outer, sep) {
		outer += sep
	}
	if runtime.GOOS == "windows" {
		return len(inner) > len(outer) && strings.EqualFold(inner[:len(outer)], outer)
	}
	return strings.HasPrefix(inner, outer)
}
