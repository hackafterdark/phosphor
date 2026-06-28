package shell

import (
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeDefaultEnv_ContainsRequiredVars(t *testing.T) {
	t.Parallel()

	env := SafeDefaultEnv()

	required := []string{
		"PATH", "HOME", "LANG", "TERM", "PAGER", "EDITOR",
		"GIT_PAGER", "JJ_PAGER", "VISUAL",
		"TERM_PROGRAM", "COLORTERM",
		"USER", "USERNAME",
		"TMP", "TEMP", "TMPDIR",
	}
	for _, v := range required {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			found := false
			for _, e := range env {
				if e == v || len(e) > len(v)+1 && e[:len(v)+1] == v+"=" {
					found = true
					break
				}
			}
			require.True(t, found, "SafeDefaultEnv should contain %s", v)
		})
	}
}

func TestSafeDefaultEnv_PlatformSpecific(t *testing.T) {
	t.Parallel()

	env := SafeDefaultEnv()

	switch runtime.GOOS {
	case "windows":
		winVars := []string{
			"SystemDrive", "SystemRoot", "ProgramFiles",
			"APPDATA", "LOCALAPPDATA", "USERPROFILE", "COMPUTERNAME",
		}
		for _, v := range winVars {
			t.Run(v, func(t *testing.T) {
				t.Parallel()
				found := false
				for _, e := range env {
					if e == v || len(e) > len(v)+1 && e[:len(v)+1] == v+"=" {
						found = true
						break
					}
				}
				require.True(t, found, "Windows SafeDefaultEnv should contain %s", v)
			})
		}

	case "darwin":
		found := false
		for _, e := range env {
			if e == "SHELL" || len(e) > 6 && e[:7] == "SHELL=" {
				found = true
				break
			}
		}
		require.True(t, found, "macOS SafeDefaultEnv should contain SHELL")
	}
}

func TestFilterEnv_Basic(t *testing.T) {
	t.Parallel()

	environ := []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"SECRET_API_KEY=abc123",
		"AWS_SECRET_ACCESS_KEY=xyz",
		"TERM=xterm-256color",
		"DEBUG=true",
	}

	allowlist := buildAllowlist([]string{"PATH", "HOME", "TERM"})
	filtered := filterEnv(environ, allowlist)

	require.ElementsMatch(t, []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"TERM=xterm-256color",
	}, filtered)
}

func TestFilterEnv_CaseInsensitive(t *testing.T) {
	t.Parallel()

	environ := []string{
		"path=/usr/bin",
		"Path=/usr/local/bin",
		"PATH=/usr/sbin",
		"home=/home/user",
		"HOME=/root",
	}

	// Allowlist with lowercase names should still match on any platform.
	allowlist := buildAllowlist([]string{"path", "home"})
	filtered := filterEnv(environ, allowlist)

	require.ElementsMatch(t, environ, filtered,
		"case-insensitive matching should let through all case variants")
}

func TestFilterEnv_EmptyAllowlist(t *testing.T) {
	t.Parallel()

	environ := []string{
		"PATH=/usr/bin",
		"SECRET=abc",
	}

	allowlist := buildAllowlist(nil)
	filtered := filterEnv(environ, allowlist)

	require.Empty(t, filtered, "empty allowlist should filter everything")
}

func TestFilterEnv_NoEqualsSign(t *testing.T) {
	t.Parallel()

	environ := []string{
		"PATH=/usr/bin",
		"BADENTRY",
		"HOME=/root",
	}

	allowlist := buildAllowlist([]string{"PATH", "HOME"})
	filtered := filterEnv(environ, allowlist)

	require.ElementsMatch(t, []string{
		"PATH=/usr/bin",
		"HOME=/root",
	}, filtered)
}

func TestFilterEnv_AllowAll(t *testing.T) {
	t.Parallel()

	environ := []string{
		"A=1",
		"B=2",
		"C=3",
	}

	allowlist := buildAllowlist([]string{"A", "B", "C"})
	filtered := filterEnv(environ, allowlist)

	require.ElementsMatch(t, environ, filtered)
}

func TestScrubProxyCreds_StripsUserinfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "https with user:pass",
			in:   "https://user:password@proxy.example.com:8080",
			want: "https://proxy.example.com:8080",
		},
		{
			name: "http with user:pass@",
			in:   "http://admin:s3cret@corporate-proxy:3128",
			want: "http://corporate-proxy:3128",
		},
		{
			name: "socks5 with credentials",
			in:   "socks5://user:pass@proxy:1080",
			want: "socks5://proxy:1080",
		},
		{
			name: "no credentials",
			in:   "https://proxy.example.com:8080",
			want: "https://proxy.example.com:8080",
		},
		{
			name: "empty value",
			in:   "",
			want: "",
		},
		{
			name: "non-URL value",
			in:   "*.local,127.0.0.1",
			want: "*.local,127.0.0.1",
		},
		{
			name: "ftp scheme (not scrubbed)",
			in:   "ftp://user:pass@host.com",
			want: "ftp://user:pass@host.com",
		},
		{
			name: "user without password",
			in:   "http://user@proxy.example.com",
			want: "http://proxy.example.com",
		},
		{
			name: "complex userinfo with special chars",
			in:   "https://user%40name:p%40ss@proxy:8080/path",
			want: "https://proxy:8080/path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scrubProxyCreds(tc.in)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestFilterEnv_ScrubsProxyCredentials(t *testing.T) {
	t.Parallel()

	environ := []string{
		"HTTPS_PROXY=https://admin:p4ss@proxy.corp:8080",
		"HTTP_PROXY=http://proxy.corp:3128",
		"NO_PROXY=localhost,127.0.0.1",
		"SECRET_API_KEY=abc123",
	}

	allowlist := buildAllowlist([]string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"})
	filtered := filterEnv(environ, allowlist)

	// SECRET_API_KEY should be filtered out entirely.
	for _, e := range filtered {
		require.NotContains(t, e, "SECRET_API_KEY")
	}

	// HTTPS_PROXY should have credentials stripped.
	var httpsProxy string
	for _, e := range filtered {
		if strings.HasPrefix(e, "HTTPS_PROXY=") {
			httpsProxy = e
			break
		}
	}
	require.NotEmpty(t, httpsProxy)
	require.NotContains(t, httpsProxy, "admin")
	require.NotContains(t, httpsProxy, "p4ss")
	require.Contains(t, httpsProxy, "proxy.corp")

	// HTTP_PROXY without credentials should be unchanged.
	var httpProxy string
	for _, e := range filtered {
		if strings.HasPrefix(e, "HTTP_PROXY=") {
			httpProxy = e
			break
		}
	}
	require.Equal(t, "HTTP_PROXY=http://proxy.corp:3128", httpProxy)
}

func TestSafeDefaultEnv_ContainsProxyVars(t *testing.T) {
	t.Parallel()

	env := SafeDefaultEnv()

	proxyVars := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
	}
	for _, v := range proxyVars {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			require.True(t, slices.Contains(env, v),
				"SafeDefaultEnv should contain %s", v)
		})
	}

	xdgVars := []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME"}
	for _, v := range xdgVars {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			require.True(t, slices.Contains(env, v),
				"SafeDefaultEnv should contain %s", v)
		})
	}
}

func TestBuildAllowlist(t *testing.T) {
	t.Parallel()

	m := buildAllowlist([]string{"PATH", "path", "Home"})

	require.Contains(t, m, "path")
	require.Contains(t, m, "home")
	require.NotContains(t, m, "PATH") // stored lowercase
	require.NotContains(t, m, "HOME")
}

func TestPhosphorEnvMarkers_Descriptive(t *testing.T) {
	t.Parallel()

	markers := PhosphorEnvMarkers()

	require.Contains(t, markers, "PHOSPHOR=1")
	require.Contains(t, markers, "PHOSPHOR_AGENT=true")
	require.Contains(t, markers, "AGENT=phosphor")
	require.Contains(t, markers, "AI_AGENT=phosphor")

	require.True(t, slices.Contains(markers, "PHOSPHOR_AGENT=true"),
		"PHOSPHOR_AGENT=true should be a marker")
}

func TestNewShell_FiltersEnvByDefault(t *testing.T) {
	t.Parallel()

	s := NewShell(&Options{WorkingDir: t.TempDir()})
	env := s.GetEnv()

	// The shell should have Phosphor markers appended.
	hasMarker := false
	for _, e := range env {
		if e == "PHOSPHOR_AGENT=true" {
			hasMarker = true
			break
		}
	}
	require.True(t, hasMarker, "shell should have PHOSPHOR_AGENT marker")

	// The shell should NOT have arbitrary host env vars like OLLAMA_MODELS
	// or HF_HOME. These are filtered by the safe default allowlist.
	for _, e := range env {
		require.NotContains(t, e, "OLLAMA_MODELS=",
			"OLLAMA_MODELS should be filtered out by safe default")
		require.NotContains(t, e, "HF_HOME=",
			"HF_HOME should be filtered out by safe default")
	}
}

func TestNewShell_UsesAllowedEnvWhenProvided(t *testing.T) {
	t.Parallel()

	s := NewShell(&Options{
		WorkingDir: t.TempDir(),
		AllowedEnv: []string{"OLLAMA_MODELS"},
	})
	env := s.GetEnv()

	found := false
	for _, e := range env {
		if len(e) > len("OLLAMA_MODELS=") && e[:len("OLLAMA_MODELS=")] == "OLLAMA_MODELS=" {
			found = true
			break
		}
	}
	require.True(t, found, "OLLAMA_MODELS should be visible when explicitly allowed")
}

func TestNewShell_ExplicitEnvOverridesFiltering(t *testing.T) {
	t.Parallel()

	customEnv := []string{"CUSTOM_VAR=hello", "OTHER=world"}
	s := NewShell(&Options{
		WorkingDir: t.TempDir(),
		Env:        customEnv,
	})
	env := s.GetEnv()

	// Explicit Env bypasses allowlist filtering, but Phosphor markers are
	// always appended so child processes can detect they're in the sandbox.
	require.Contains(t, env, "CUSTOM_VAR=hello")
	require.Contains(t, env, "OTHER=world")
	require.Contains(t, env, "PHOSPHOR_AGENT=true")
}
