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
	// Clean("") yields ".", which would then match a "." candidate as
	// "the workspace itself". Preserve the empty value so the workspace
	// rule is skipped exactly as documented.
	ws := workspaceRoot
	if ws != "" {
		ws = filepath.Clean(ws)
	}

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
	// Reference paths are canonicalized once: on macOS t.TempDir-style
	// references name /var/... while canonical resolution yields
	// /private/var/..., and comparisons must see through the alias on
	// the reference side. The candidate itself is *not* canonicalized
	// here — symlink laundering is surfaced by the caller's explicit
	// resolution pass (ValidateTrustedExtraRoots) so the refusal names
	// the resolved target, rather than silently rewriting the entry.
	if workspaceRoot != "" {
		if resolved, err := filepath.EvalSymlinks(workspaceRoot); err == nil {
			workspaceRoot = filepath.Clean(resolved)
		}
	}
	if home != "" {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = filepath.Clean(resolved)
		}
	}
	if temp != "" {
		if resolved, err := filepath.EvalSymlinks(temp); err == nil {
			temp = filepath.Clean(resolved)
		}
	}
	for _, sensitive := range canonicalVariants(sensitiveRoots) {
		if samePath(clean, sensitive) {
			return true, fmt.Sprintf("it is the well-known system directory %q", clean)
		}
	}
	for _, sensitive := range canonicalVariants(sensitiveEnvDirs()) {
		if samePath(clean, sensitive) {
			return true, fmt.Sprintf("it is the host system directory %q", clean)
		}
	}
	if homeErr == nil && home != "" && samePath(clean, filepath.Clean(home)) {
		return true, "it is the user home directory, which contains the credential deny-list (SSH keys, cloud tokens, git credentials)"
	}
	if temp != "" && samePath(clean, temp) {
		return true, "it is the OS temporary directory, which is already trusted by default"
	}
	if workspaceRoot != "" {
		if samePath(clean, workspaceRoot) {
			return true, "it is the workspace itself, which is already trusted by default"
		}
		if insidePath(workspaceRoot, clean) {
			return true, fmt.Sprintf("it is an ancestor of the workspace (%q), which disables the workspace confinement", workspaceRoot)
		}
	}
	for _, deny := range CredentialDenyPaths() {
		deny = filepath.Clean(deny)
		if insidePath(deny, clean) {
			return true, fmt.Sprintf("it contains credential deny-list path %q, which this backend cannot enforce inside an allowed root", deny)
		}
		if insidePath(clean, deny) {
			return true, fmt.Sprintf("it is inside credential deny-list path %q, which is unreadable in the sandbox", deny)
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
	// UNC share roots are recognized on every platform, mirroring the
	// drive-letter rule: filepath.VolumeName only parses them on Windows,
	// and the refusal must hold wherever the config is read.
	if strings.HasPrefix(cleanPath, `\\`) {
		return true
	}
	if vol := filepath.VolumeName(cleanPath); vol != "" {
		rest := cleanPath[len(vol):]
		return rest == "" || rest == `\` || rest == "/"
	}
	return cleanPath == "/" || cleanPath == `\`
}

// IsVolumeRootSpelling reports whether dir is *spelled* as a filesystem,
// drive, or UNC root, checked lexically on every platform. filepath.Abs
// uses the host's path semantics, so on POSIX it resolves "C:\" relative to
// the working directory instead of recognizing it as a volume root; callers
// validating raw config entries must check the spelling before resolving.
func IsVolumeRootSpelling(dir string) bool {
	if dir == "" {
		return false
	}
	return isVolumeRoot(filepath.Clean(normalizeRoot(dir)))
}

// canonicalVariants returns the lexical spellings plus canonical targets
// of the given policy paths, so hosts that alias sensitive directories
// (macOS /etc -> /private/etc) refuse both spellings. Paths that do not
// exist on this host resolve to themselves and are compared lexically.
func canonicalVariants(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, dir)
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if resolved = filepath.Clean(resolved); resolved != dir {
				out = append(out, resolved)
			}
		}
	}
	return out
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
