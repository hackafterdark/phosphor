package shell

import (
	"runtime"
	"strings"
)

// safeDefaultEnvVars is the minimum set of environment variables needed for
// typical development shell commands to function correctly. This is used as
// the fallback allowlist when no user-defined allowed_env is configured.
//
// The variables are intentionally minimal: PATH and HOME are required for
// command resolution and home-relative paths; LANG/LANG/COLORTERM control
// locale and color output; TMP/TEMP/TMPDIR are needed by many build tools;
// USER/USERNAME is used by prompts, git, and editors. XDG_* vars are needed
// by modern Linux tools; *_PROXY vars are needed for network access but
// credentials embedded in their values are scrubbed (see scrubProxyCreds).
var safeDefaultEnvVars = []string{
	"PATH",
	"HOME",
	"LANG",
	"TERM",
	"PAGER",
	"EDITOR",
	"GIT_PAGER",
	"JJ_PAGER",
	"VISUAL",
	"TERM_PROGRAM",
	"COLORTERM",
	"USER",
	"USERNAME",
	"TMP",
	"TEMP",
	"TMPDIR",
	// XDG base directory spec — used by modern Linux tools for config/data
	// location resolution.
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	// Proxy variables — needed for outbound network access through corporate
	// or personal proxies. Credentials embedded in the URL (user:pass@host)
	// are scrubbed by scrubProxyCreds before being passed to the agent.
	"HTTP_PROXY",
	"http_proxy",
	"HTTPS_PROXY",
	"https_proxy",
	"NO_PROXY",
	"no_proxy",
}

// SafeDefaultEnv returns the minimum environment variables needed for
// typical development shell commands to function. This is used when no
// user-defined allowlist is configured. The returned slice is a fresh
// allocation safe to use concurrently with the input.
func SafeDefaultEnv() []string {
	base := make([]string, len(safeDefaultEnvVars))
	copy(base, safeDefaultEnvVars)

	switch runtime.GOOS {
	case "windows":
		base = append(base,
			"SystemDrive",
			"SystemRoot",
			"ProgramFiles",
			"ProgramFiles(x86)",
			"APPDATA",
			"LOCALAPPDATA",
			"USERPROFILE",
			"COMPUTERNAME",
		)
	case "darwin":
		base = append(base, "SHELL")
	}
	return base
}

// filterEnv returns only the entries from environ whose key appears in the
// allowlist. Keys are matched case-insensitively on Windows, where the
// environment is case-insensitive, so that an allowlist of lowercase names
// still lets through variables stored with different casing. Values for
// proxy variables are scrubbed of embedded credentials (see scrubProxyCreds).
func filterEnv(environ []string, allowlist map[string]struct{}) []string {
	var filtered []string
	for _, e := range environ {
		key, val, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, allowed := allowlist[strings.ToLower(key)]; !allowed {
			continue
		}
		filtered = append(filtered, key+"="+scrubProxyCreds(val))
	}
	return filtered
}

// buildAllowlist converts a slice of variable names into a map suitable for
// use with filterEnv. Names are lowercased so that on Windows the lookup is
// case-insensitive without requiring per-entry ToLower calls at filter time.
func buildAllowlist(names []string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[strings.ToLower(n)] = struct{}{}
	}
	return m
}

// scrubProxyCreds removes embedded user credentials from proxy URL values.
// A value like "http://user:password@proxy.example.com:8080" becomes
// "http://proxy.example.com:8080". This prevents secrets from leaking into
// the agent's shell environment when proxy variables are passed through the
// allowlist. Non-URL values and values without embedded credentials are
// returned unchanged. Empty values are returned as-is.
//
// The implementation uses a simple string scan rather than net/url parsing
// because proxy env vars may contain comma-separated host lists (NO_PROXY)
// or other non-URL content that net/url would reject. We only strip the
// userinfo portion when a URL scheme is present and the pattern
// "scheme://user:pass@" is detected.
func scrubProxyCreds(val string) string {
	if val == "" {
		return val
	}

	schemeEnd := strings.IndexByte(val, ':')
	if schemeEnd < 0 {
		return val
	}
	scheme := val[:schemeEnd]

	// Only scrub values that look like URLs with a known scheme.
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return val
	}

	// Find "://" after the scheme.
	protoStart := schemeEnd + 1
	if protoStart+1 >= len(val) || val[protoStart] != '/' || val[protoStart+1] != '/' {
		return val
	}

	// Search for "@" after the "//" — this marks the start of host.
	// Everything before it (but after "//") is userinfo.
	atIdx := strings.IndexByte(val[protoStart+2:], '@')
	if atIdx < 0 {
		return val
	}

	// Reconstruct without userinfo.
	return val[:protoStart+2] + val[protoStart+2+atIdx+1:]
}
