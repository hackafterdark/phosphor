package shell

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkBlocker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		networkCommands []string
		policy          NetworkPolicy
		args            []string
		shouldBlock     bool
	}{
		{
			name:            "disabled blocks known network command",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{},
			args:            []string{"curl", "https://example.com"},
			shouldBlock:     true,
		},
		{
			name:            "enabled allows known network command when host allowlist is empty",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{Enabled: true},
			args:            []string{"curl", "https://example.com"},
			shouldBlock:     false,
		},
		{
			name:            "enabled blocks network command absent from allowed commands",
			networkCommands: []string{"curl", "wget"},
			policy:          NetworkPolicy{Enabled: true, AllowedCommands: []string{"wget"}},
			args:            []string{"curl", "https://example.com"},
			shouldBlock:     true,
		},
		{
			name:            "enabled allows network command present in allowed commands",
			networkCommands: []string{"curl", "wget"},
			policy:          NetworkPolicy{Enabled: true, AllowedCommands: []string{"wget"}},
			args:            []string{"wget", "https://example.com"},
			shouldBlock:     false,
		},
		{
			name:            "enabled blocks command path by normalized base name",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{Enabled: true, AllowedCommands: []string{"wget"}},
			args:            []string{"/usr/bin/curl.exe", "https://example.com"},
			shouldBlock:     true,
		},
		{
			name:            "enabled allows command path when base name is allowed",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"curl"},
				HostAllowlist:   []string{"example.com"},
			},
			args:        []string{"/usr/bin/curl.exe", "https://example.com"},
			shouldBlock: false,
		},
		{
			name:            "enabled blocks disallowed host",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{".example.com"},
			},
			args:        []string{"curl", "https://example.org"},
			shouldBlock: true,
		},
		{
			name:            "enabled allows suffix host",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{".example.com"},
			},
			args:        []string{"curl", "https://sub.example.com"},
			shouldBlock: false,
		},
		{
			name:            "enabled allows exact host",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{"example.org"},
			},
			args:        []string{"curl", "https://example.org"},
			shouldBlock: false,
		},
		{
			name:            "enabled allows wildcard suffix host",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{"*.example.com"},
			},
			args:        []string{"curl", "https://api.example.com"},
			shouldBlock: false,
		},
		{
			name:            "private IPv4 is blocked",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{"10.0.0.5"},
			},
			args:        []string{"curl", "http://10.0.0.5/"},
			shouldBlock: true,
		},
		{
			name:            "metadata IPv4 is blocked",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{"169.254.169.254"},
			},
			args:        []string{"curl", "http://169.254.169.254/latest/meta-data"},
			shouldBlock: true,
		},
		{
			name:            "metadata hostname is blocked",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:       true,
				HostAllowlist: []string{"metadata.google.internal"},
			},
			args:        []string{"curl", "http://metadata.google.internal/"},
			shouldBlock: true,
		},
		{
			name:            "loopback hostname is blocked",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{Enabled: true},
			args:            []string{"curl", "http://localhost/"},
			shouldBlock:     true,
		},
		{
			name:            "loopback IPv6 is blocked",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{Enabled: true},
			args:            []string{"curl", "http://[::1]/"},
			shouldBlock:     true,
		},
		{
			name:            "private IPv6 is blocked",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{Enabled: true},
			args:            []string{"curl", "http://[fd00::1]/"},
			shouldBlock:     true,
		},
		{
			name:            "non network command is unaffected by host strings",
			networkCommands: []string{"curl"},
			policy:          NetworkPolicy{Enabled: true},
			args:            []string{"echo", "https://evil.com"},
			shouldBlock:     false,
		},
		{
			name:            "non network command listed in allowed commands is host checked",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"git"},
				HostAllowlist:   []string{".github.com"},
			},
			args:        []string{"git", "clone", "https://evil.com/repo.git"},
			shouldBlock: true,
		},
		{
			name:            "non network command listed in allowed commands can reach allowed host",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"git"},
				HostAllowlist:   []string{".github.com"},
			},
			args:        []string{"git", "clone", "https://github.com/phosphor/phosphor.git"},
			shouldBlock: false,
		},
		{
			name:            "scp user at host is blocked by private destination",
			networkCommands: []string{"scp"},
			policy:          NetworkPolicy{Enabled: true},
			args:            []string{"scp", "user@10.0.0.5:file"},
			shouldBlock:     true,
		},
		{
			name:            "nc without destination is allowed when enabled",
			networkCommands: []string{"nc"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"nc"},
			},
			args:        []string{"nc", "-l", "-p", "4444"},
			shouldBlock: false,
		},
		{
			name:            "curl output flag value is not treated as destination",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"curl"},
				HostAllowlist:   []string{"example.com"},
			},
			args:        []string{"curl", "-o", "report.pdf", "https://example.com"},
			shouldBlock: false,
		},
		{
			name:            "ambiguous host-like argument is blocked when not allowed",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"curl"},
				HostAllowlist:   []string{"example.com"},
			},
			args:        []string{"curl", "file.txt"},
			shouldBlock: true,
		},
		{
			name:            "curl url flag value is checked",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"curl"},
				HostAllowlist:   []string{"example.com"},
			},
			args:        []string{"curl", "--url=https://example.com"},
			shouldBlock: false,
		},
		{
			name:            "curl url flag value is blocked when disallowed",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"curl"},
				HostAllowlist:   []string{"example.com"},
			},
			args:        []string{"curl", "--url=https://evil.com"},
			shouldBlock: true,
		},
		{
			name:            "single label user at host can be allowed",
			networkCommands: []string{"curl"},
			policy: NetworkPolicy{
				Enabled:         true,
				AllowedCommands: []string{"curl"},
				HostAllowlist:   []string{"internal"},
			},
			args:        []string{"curl", "user@internal:8080"},
			shouldBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocker := NetworkBlocker(tt.networkCommands, tt.policy)
			require.Equal(t, tt.shouldBlock, blocker(tt.args), "args %v", tt.args)
		})
	}
}
