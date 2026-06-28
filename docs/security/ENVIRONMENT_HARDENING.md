# Environment Variable Hardening

## Threat Model

The Phosphor agent executes shell commands on behalf of the user. Without
confinement, a prompt-injected or misbehaving agent could:

- Leak secrets by reading environment variables (e.g. `AWS_SECRET_ACCESS_KEY`,
  `OLLAMA_MODELS`, API tokens, SSH keys).
- Enumerate the host environment via `env`, `set`, `printenv`, or
  `/proc/self/environ`.
- Exfiltrate sensitive values through base64 encoding or outbound network
  requests.
- Manipulate the host shell state via `export`, `set`, or `stty`.

The goal of this hardening is to ensure that **every shell the agent spawns
sees only a minimal, well-defined set of environment variables**. Anything
not explicitly needed is invisible — not just hidden from the agent's view,
but absent from the process environment entirely.

## Architecture: Three Layers of Defense

```
┌───────────────────────────────────────────────────────────┐
│  Layer 3: Command Deny List (blockFuncs)                  │
│  External commands matched by BlockFunc are rejected      │
│  before they reach the exec handler. Logging is emitted   │
│  for every blocked command to aid debugging.              │
├───────────────────────────────────────────────────────────┤
│  Layer 2: Environment Allowlist (filterEnv)               │
│  os.Environ() is filtered through a user-configurable or  │
│  safe-default allowlist before being passed to the shell. │
│  On Windows, matching is case-insensitive.                │
├───────────────────────────────────────────────────────────┤
│  Layer 1: Sandbox Markers (PhosphorEnvMarkers)            │
│  Every spawned shell receives PHOSPHOR_AGENT=true so      │
│  child processes and scripts can detect they are running  │
│  inside the agent sandbox and behave accordingly.         │
└───────────────────────────────────────────────────────────┘
         │
         ▼
  internal/shell/shell.go → NewShell()
  (the single enforcement point for all shell surfaces)
```

## Layer 2: Environment Filtering

**Location:** `internal/shell/env.go`, `internal/shell/shell.go`

When `NewShell` is called without an explicit `Env` slice, the process
environment (`os.Environ()`) is filtered through an allowlist. Only variables
whose names appear in the allowlist are passed to the spawned shell.

### Safe Default Allowlist

When no user configuration is provided, Phosphor uses a minimal set of
variables required for typical development commands to function:

```
PATH, HOME, LANG, TERM, PAGER, EDITOR, GIT_PAGER, JJ_PAGER, VISUAL,
TERM_PROGRAM, COLORTERM, USER, USERNAME, TMP, TEMP, TMPDIR,
XDG_CONFIG_HOME, XDG_DATA_HOME,
HTTP_PROXY, HTTPS_PROXY, NO_PROXY (and lowercase variants)
```

Platform-specific additions:

| Platform | Additional Variables                              |
|----------|---------------------------------------------------|
| Windows  | `SystemDrive`, `SystemRoot`, `ProgramFiles`,      |
|          | `ProgramFiles(x86)`, `APPDATA`, `LOCALAPPDATA`,   |
|          | `USERPROFILE`, `COMPUTERNAME`                     |
| macOS    | `SHELL`                                           |

Variables like `OLLAMA_MODELS`, `HF_HOME`, `AWS_SECRET_ACCESS_KEY`,
`SECRET_API_KEY`, and any other host-specific values are **not** visible to
the agent.

> **Credential scrubbing:** Proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`,
> `NO_PROXY` and their lowercase variants) may contain embedded credentials
> in the URL (e.g. `https://user:password@proxy.example.com:8080`). Phosphor
> strips the `userinfo@` portion of these values before passing them to the
> agent shell. The proxy hostname and port are preserved so network access
> continues to work; if a proxy requires authentication, users should use
> `.netrc`, a credential manager, or a proxy configuration file instead of
> embedding credentials in environment variables.

### User-Configured Allowlist

Users can override the default via `tools.bash.allowed_env` in
`phosphor.json`:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "allowed_env": ["PATH", "HOME", "AWS_PROFILE"]
    }
  }
}
```

When `allowed_env` is set (even if empty), the safe default is **not** used —
only the explicitly listed variables are passed through. To combine the safe
default with additional variables, list them all explicitly.

### Case-Insensitive Matching on Windows

Windows environment variables are case-insensitive. The allowlist matching
uses `strings.ToLower()` on both the allowlist keys and the environment
variable names, so a config entry of `"path"` will correctly match an env
entry of `"Path"` or `"PATH"`.

### Explicit Environment Override

Callers that provide an explicit `Env` slice to `NewShell` bypass filtering
entirely. This is used by the hook runner and other internal surfaces that
need full control over the environment. Regardless of the source,
`PhosphorEnvMarkers()` are always appended so child processes can detect the
sandbox.

## Layer 3: Command Deny List

**Location:** `internal/shell/run.go` (blockHandler), `internal/agent/tools/bash.go` (blockFuncs)

The `blockHandler` middleware intercepts external command execution in the
mvdan/sh interpreter chain. Each `BlockFunc` in the list is tested against
the resolved argv; if any returns true, the command is rejected with a
security error and an info log entry is emitted:

```
INFO Command blocked by security policy  command=curl  args="[curl http://example.com]"
```

This logging is essential for debugging overbroad deny rules — if a user
accidentally bans a legitimate tool, the log entry makes it easy to identify
and relax the rule.

### Built-In Deny List

The following commands are blocked by default and cannot be overridden:

- **Network tools**: `curl`, `wget`, `nc`, `ssh`, `scp`, `telnet`, `chrome`,
  `firefox`, `links`, `lynx`, `w3m`
- **Privilege escalation**: `sudo`, `su`, `doas`
- **Package managers**: `apt`, `dnf`, `pacman`, `brew`, `cargo`, `gem`,
  `npm`, `pip`, etc. (with specific argument patterns like `install`, `-g`,
  `--global`)
- **System modification**: `systemctl`, `mount`, `fdisk`, `mkfs`, `crontab`
- **Network config**: `iptables`, `firewall-cmd`, `ufw`, `ip`, `ifconfig`
- **Self-execution**: `go run .`, `go build .`, `phosphor` (prevents the
  agent from spawning another Phosphor instance)
- **Go test exec**: `go test -exec` (prevents arbitrary command injection
  through Go's test infrastructure)
- **Interpreter code execution**: `python -c`, `python3 -c`, `node -e`,
  `perl -e`, `ruby -e`, `php -r`, `lua -e`, `bash -c`, `sh -c`, `zsh -c`,
  `ksh -c`, `dash -c` — blocks inline code execution flags on interpreters
  and shells. These flags bypass every shell-level defense (env filtering,
  command blocking, workspace bounds) by executing arbitrary code in another
  runtime. Normal script invocation (`python script.py`, `node build.js`)
  is preserved.

### Interpreter Code Execution Bypass

An agent can circumvent all shell-level security controls by invoking an
interpreter with an inline code execution flag:

```bash
python -c "import os; print(os.environ)"    # leaks all env vars
node -e "console.log(process.env)"          # leaks all env vars
perl -e 'print join("\n", keys %ENV)'       # leaks all env var names
ruby -e "ENV.each { |k,v| puts k }"         # leaks all env var names
bash -c 'env'                               # runs env in a sub-shell
```

These commands are external binaries that reach the exec handler chain and
are blocked by argument matchers before they execute. The block targets only
the code-execution flags (`-c`, `-e`, `-r`); running scripts from files
(`python my_script.py`, `node build.js`) is unaffected.

### Disabling Interpreter Code Execution Blockers

If you need the agent to invoke interpreters with inline code execution
flags (e.g. for rapid prototyping or testing), set
`tools.bash.allow_inline_execution` to `true`:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "allow_inline_execution": true
    }
  }
}
```

When enabled, the following argument matchers are removed:

- `python -c`, `python3 -c`, `python2 -c`
- `node -e`
- `perl -e`
- `ruby -e`
- `php -r`
- `lua -e`
- `bash -c`, `sh -c`, `zsh -c`, `ksh -c`, `dash -c`

All other security controls (env filtering, command blocking, workspace
bounds, credential scrubbing) remain active. Only the interpreter/shell
code-execution flag blockers are lifted.

### User-Configured Deny List

Users can extend the built-in list via `tools.bash.banned_commands`:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "banned_commands": ["netcat", "powershell", "set"]
    }
  }
}
```

This is an append-only extension — user entries are concatenated after the
built-in list. Built-in entries cannot be removed via configuration.

### Cross-Platform Considerations

| Platform | Recommended Additions                                           |
|----------|-----------------------------------------------------------------|
| Windows  | `set`, `setx`, `powershell`, `pwsh`, `cmd`, `reg`, `wmic`       |
| macOS    | `launchctl`, `sysctl`, `dmesg`                                  |
| Linux    | `stty`                                                          |
| All      | `env`, `printenv`, `compgen` (list all environment variables)   |

## Layer 1: Sandbox Markers

**Location:** `internal/shell/shell.go` (`PhosphorEnvMarkers`)

Every shell spawned by Phosphor receives the following environment markers:

```
PHOSPHOR=1
PHOSPHOR_AGENT=true
AGENT=phosphor
AI_AGENT=phosphor
```

These markers are always appended, regardless of whether the environment was
filtered or explicitly provided. They serve two purposes:

1. **Detection**: Scripts and tools running inside the agent sandbox can
   check for `PHOSPHOR_AGENT=true` to determine they are in a constrained
   environment and adjust their behavior accordingly (e.g. skipping
   interactive prompts, avoiding network access).
2. **Consistency**: Keeping markers in a single function (`PhosphorEnvMarkers`)
   guarantees that both the bash tool's `Shell` and the hook runner's `Run`
   calls cannot drift apart.

## Configuration Reference

### `tools.bash.allowed_env`

```yaml
type: []string
default: null (uses safe default)
description: >
  Allowlist of environment variable names the bash tool may see. When empty
  or unset, a safe default is used (PATH, HOME, TERM, XDG_CONFIG_HOME,
  HTTP_PROXY, etc.) that covers typical development needs while excluding
  secrets and sensitive host state. Matching is case-insensitive on Windows.
  Proxy variable values (HTTP_PROXY, HTTPS_PROXY, NO_PROXY) are scrubbed of
  embedded credentials (user:pass@host) before being passed to the agent.
```

### `tools.bash.banned_commands`

```yaml
type: []string
default: null (uses built-in defaults only)
description: >
  Additional command names to block beyond the built-in defaults. Entries are
  matched against argv[0] (the command name). Supports cross-platform entries
  such as "set" for Windows or "launchctl" for macOS. This is an append-only
  extension — built-in entries cannot be removed.
```

### `tools.bash.allow_inline_execution`

```yaml
type: bool
default: false
description: >
  When true, permits interpreters and shells to be invoked with inline code
  execution flags (-c, -e, -r). When false (default), commands like `python
  -c`, `node -e`, `perl -e`, `bash -c` are blocked because they bypass
  shell-level security controls by executing arbitrary code in another
  runtime. Normal script invocation (python script.py, node build.js) is
  unaffected either way.

  **Auditing:** When set to true, every interpreter/shell invocation with an
  inline code flag emits a Warn-level slog log entry ("Interpreter shell code
  execution allowed by config") and an OpenTelemetry span event
  (`inline_execution_allowed`) on the bash tool-call span, carrying the
  interpreter command and full argv as attributes. The span also includes the
  `phosphor.security.inline_execution_allowed=true` attribute for easy
  filtering in trace dashboards.
```

### Observability: Inline Execution Tracing

When `allow_inline_execution` is enabled, the bash tool-call otel span records:

| Signal | Detail |
|--------|--------|
| `slog.Warn` log record | `"Interpreter shell code execution allowed by config"` with `command` and `args` fields |
| Span event | `inline_execution_allowed` with `interpreter.command` and `interpreter.args` attributes |
| Span attribute | `phosphor.security.inline_execution_allowed=true` (set once per tool call) |

This lets operators query traces for "which commands ran with inline code execution?" and alert on the warn-level log entries. Example OpenSearch/Kibana query:

```
level: warn AND "Interpreter shell code execution allowed by config"
```

## Example Configurations

### Minimal (safe defaults)

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {}
}
```

The agent sees only `PATH`, `HOME`, `LANG`, `TERM`, etc. No secrets, no
`OLLAMA_MODELS`, no `HF_HOME`.

### Development with AWS

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "allowed_env": ["PATH", "HOME", "LANG", "TERM", "AWS_PROFILE", "AWS_REGION"]
    }
  }
}
```

The agent can read `AWS_PROFILE` and `AWS_REGION` but not
`AWS_SECRET_ACCESS_KEY` or `AWS_SESSION_TOKEN`.

### Restricted (no network tools)

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "banned_commands": ["curl", "wget", "nc", "ssh", "scp"]
    }
  }
}
```

These are already in the built-in list, but explicit configuration makes the
intent clear and ensures they remain blocked even if built-in defaults change.

### Windows-specific

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "banned_commands": ["powershell", "pwsh", "cmd", "set", "reg"]
    }
  }
}
```

## Debugging Blocked Commands

When a command is blocked, Phosphor emits an info log entry:

```
INFO Command blocked by security policy  command=netcat  args="[netcat]"
```

To find blocked commands in your logs, search for `Command blocked by security
policy`. The `command` and `args` fields identify what was blocked; if the
block is unexpected, add the command name to `banned_commands` explicitly to
make the intent clear, or remove it from the list if it was accidentally
included.

## What Is NOT Blocked

The deny list operates at the **external command** level via the mvdan/sh
exec handler chain. Shell builtins (e.g. `echo`, `cd`, `export`, `set`,
`read`) are handled by the builtin handler and are not intercepted. This is
by design — many builtins are essential for normal shell operation.

Environment variable leakage through builtins (e.g. `export SECRET=foo`) is
mitigated by Layer 2 (environment filtering): the agent simply cannot see
the secret in the first place, so there is nothing to export.

Shell state manipulation via `stty` or `set -x` (which would echo variables)
is also mitigated by the filtered environment — the variables are absent, so
echoing them reveals nothing.

## Testing

Comprehensive tests are in `internal/shell/env_test.go` and
`internal/agent/tools/bash_test.go`:

- `TestFilterEnv_Basic` — basic allowlist filtering
- `TestFilterEnv_CaseInsensitive` — Windows case-insensitive matching
- `TestFilterEnv_EmptyAllowlist` — empty allowlist filters everything
- `TestSafeDefaultEnv_ContainsRequiredVars` — all required vars present
- `TestSafeDefaultEnv_PlatformSpecific` — Windows/macOS additions
- `TestPhosphorEnvMarkers_Descriptive` — sandbox markers present
- `TestNewShell_FiltersEnvByDefault` — default filtering excludes secrets
- `TestBashTool_ConfigAwareBannedCommands` — user config extends deny list
- `TestBashTool_AllowedEnv_FiltersEnvironment` — env vars invisible to agent

Run with:

```bash
go test ./internal/shell/ ./internal/agent/tools/ -run TestFilterEnv|TestSafeDefaultEnv|TestPhosphorEnvMarkers|TestNewShell|TestBashTool_ConfigAwareBannedCommands
```
