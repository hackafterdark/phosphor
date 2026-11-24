# Tools & Permissions

## Permissions

Tools listed in `permissions` run without a permission prompt:

```json
{
  "permissions": {
    "allowed_tools": ["view", "grep", "glob", "ls", "structural_search"],
    "allow_commands": ["git", "gh", "go build", "go test", "go vet"]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `allowed_tools` | string[] | Tools that skip permission prompts |
| `allow_commands` | string[] | Shell commands auto-approved by prefix |

`allow_commands` auto-approves shell commands whose every program matches an
entry (a bare name covers the program, a prefix like `go build` covers the
subcommand); compound commands prompt unless every part matches.

These options control the *prompt* only. They never relax the sandbox,
pathguard, or escalation checks. For how the consent and enforcement layers
fit together — including the argument-agnostic "Allow for this session"
grant — see
[Permission Prompts](../security/CONFIGURATION.md#permission-prompts). For a
copy-paste high-trust setup with prompt-free toolchain and git/gh loops,
see [VIBE_CODING.md](../VIBE_CODING.md).

## Tool Security Knobs

The `tools` section configures per-tool security posture:

```json
{
  "tools": {
    "bash": {
      "allowed_env": ["PATH", "HOME", "GOPATH", "PATH_TO_TOOL"],
      "banned_commands": ["shutdown"],
      "allow_inline_execution": false,
      "trusted_extra_roots": ["D:/shared-build-cache"],
      "sandbox_mode": "workspace",
      "sandbox_limits": { "max_procs": 512, "max_file_mb": 1024 },
      "trust_toolchain_caches": false,
      "network": {
        "enabled": true,
        "allowed_commands": ["git", "ping"],
        "host_allowlist": ["github.com"]
      }
    },
    "web_fetch": {
      "ip_allow_list": ["192.168.0.0/24", "127.0.0.1"],
      "allow_raw_ips": false
    },
    "ls": { "max_depth": 10, "max_items": 500 },
    "grep": { "timeout": 10000000000 }
  }
}
```

(`grep.timeout` is a Go duration in nanoseconds — `10000000000` = 10s.)

### `tools.bash`

| Field | Type | Description |
|-------|------|-------------|
| `allowed_env` | string[] | Environment variables that survive into the sandbox |
| `banned_commands` | string[] | Additional commands to block |
| `allow_inline_execution` | bool | `true` allows `python -c …`-style inline execution |
| `trusted_extra_roots` | string[] | Additional paths the sandbox trusts |
| `sandbox_mode` | string | Kernel sandbox mode for bash children — see below |
| `sandbox_limits` | object | Process/file limits for kernel sandbox |
| `trust_toolchain_caches` | bool | `true` allows toolchain cache access |
| `network.enabled` | bool | `true` enables network access for bash |
| `network.allowed_commands` | string[] | Commands allowed to use network |
| `network.host_allowlist` | string[] | Hosts reachable via network |

#### `sandbox_mode` values

| Value | Behavior |
|-------|----------|
| `"off"` | No kernel sandbox; commands run directly |
| `"workspace"` | (default) Apply the kernel sandbox; if unavailable, degrade to `off` with a warning |
| `"strict"` | Apply the kernel sandbox; refuse to run if it is unavailable |

### `tools.web_fetch`

| Field | Type | Description |
|-------|------|-------------|
| `ip_allow_list` | string[] | CIDR ranges or IPs to allow (empty = no restriction) |
| `allow_raw_ips` | bool | `true` allows direct IP access; `false` requires hostname resolution |

### `tools.ls` / `tools.grep`

| Field | Type | Description |
|-------|------|-------------|
| `ls.max_depth` | int | Maximum directory traversal depth |
| `ls.max_items` | int | Maximum items returned |
| `grep.timeout` | int | Timeout in nanoseconds |

## Disabled Tools

Completely disable a tool:

```json
{
  "options": {
    "disabled_tools": ["sourcegraph"]
  }
}
```

## Related Docs

- [security/CONFIGURATION.md](../security/CONFIGURATION.md) — Every security control
- [security/ENVIRONMENT_HARDENING.md](../security/ENVIRONMENT_HARDENING.md) — Environment hardening
- [security/NETWORK_EGRESS_HARDENING.md](../security/NETWORK_EGRESS_HARDENING.md) — Network egress
