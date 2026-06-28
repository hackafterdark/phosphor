# Security Validation Test Suite

Comprehensive regression tests ensuring security hardening controls remain effective as the codebase evolves. These tests validate that banned patterns are consistently blocked and security violations are returned.

## Architecture Alignment

Tests map to the six-layer defense architecture documented in [WORKSPACE_HARDENING.md](./WORKSPACE_HARDENING.md), [ENVIRONMENT_HARDENING.md](./ENVIRONMENT_HARDENING.md), and [NETWORK_EGRESS_HARDENING.md](./NETWORK_EGRESS_HARDENING.md):

```
┌─────────────────────────────────────────────────────────────┐
│  Fail-Closed: No Panic on Garbage Input                     │
│  → TestFuzzing_IsInsideHandlesGarbagePaths                  │
│  → TestFuzzing_IsInsideHandlesEmptyAndSpecialPaths          │
│  → TestFuzzing_BlockFuncHandlesGarbageArgs                  │
│  → TestFuzzing_BlockFuncHandlesEmptyAndSpecialCommands      │
│  → TestFuzzing_WriteToolHandlesGarbagePaths                 │
├─────────────────────────────────────────────────────────────┤
│  Layer 5: Startup Symlink Resolution                        │
│  → TestPathObfuscation_SymlinkTraversal                     │
├─────────────────────────────────────────────────────────────┤
│  Layer 6: Network Egress Hardening                          │
│  → TestWebFetchHardeningIPBlock                             │
│  → TestWebFetchHardeningCIDRAllow                           │
│  → TestWebFetchHardeningLocalhostAlwaysAllowed              │
│  → TestWebFetchHardeningUserPromptVerified                  │
│  → TestWebFetchHardeningUserPromptDenyVerified              │
├─────────────────────────────────────────────────────────────┤
│  Layer 4: Shell CWD Lockdown                                │
│  → TestCompositionalChains_WorkingDirEscapeInChain          │
│  → TestWorkspaceHardening_BashWorkingDirBlocksOutsidePaths  │
├─────────────────────────────────────────────────────────────┤
│  Layer 3: Bash working_dir guard                            │
│  → TestWorkspaceHardening_BashWorkingDirBlocksOutsidePaths  │
├─────────────────────────────────────────────────────────────┤
│  Layer 2: Per-tool path bounds checks                       │
│  → TestWorkspaceHardening_* (write, edit, multiedit, append │
│    view, download, ls, glob, grep, structural_search)       │
│  → TestAgentjacking_MCPContentAsPath                        │
│  → TestPathObfuscation_ComplexTraversal                     │
│  → TestPathObfuscation_AbsolutePathVariants                 │
├─────────────────────────────────────────────────────────────┤
│  Layer 1: Command deny list + env filtering                 │
│  → TestEnvironmentHardening_CommandBlockingViaBlockFuncs    │
│  → TestEnvironmentHardening_ArgumentsBlocker                │
│  → TestEnvironmentHardening_SelfExecBlocker                 │
│  → TestAgentjacking_MCPContentAsCommand                     │
│  → TestMemoryPoisoning_BashToolSecurity                     │
└─────────────────────────────────────────────────────────────┘
         │
         ▼
  Compositional + Persistence Tests (cross-layer)
```

## Test Files

### `internal/agent/tools/workspace_hardening_test.go`

Validates that every filesystem-touching tool blocks out-of-workspace paths with a `Security violation: path ... is outside workspace` error.

| Test | Tools Covered | Attack Vector |
|------|---------------|---------------|
| `TestWorkspaceHardening_WriteToolBlocksOutsidePaths` | write | parent escape, absolute outside path, deep traversal (`../../etc/passwd`) |
| `TestWorkspaceHardening_EditToolBlocksOutsidePaths` | edit | parent escape, absolute outside path |
| `TestWorkspaceHardening_MultiEditToolBlocksOutsidePaths` | multiedit | parent escape, absolute outside path |
| `TestWorkspaceHardening_AppendToolBlocksOutsidePaths` | append | parent escape, absolute outside path |
| `TestWorkspaceHardening_ViewToolBlocksOutsidePaths` | view | parent escape (`../etc/passwd`), absolute outside path |
| `TestWorkspaceHardening_DownloadToolBlocksOutsidePaths` | download | parent escape, absolute outside path |
| `TestWorkspaceHardening_LsToolBlocksOutsidePaths` | ls | parent escape (`../etc`), absolute outside dir |
| `TestWorkspaceHardening_GlobToolBlocksOutsidePaths` | glob | parent escape, absolute outside dir |
| `TestWorkspaceHardening_GrepToolBlocksOutsidePaths` | grep | parent escape, absolute outside dir |
| `TestWorkspaceHardening_StructuralSearchToolBlocksOutsidePaths` | structural_search | parent escape, absolute outside dir |
| `TestWorkspaceHardening_BashWorkingDirBlocksOutsidePaths` | bash | out-of-workspace `working_dir` parameter |
| `TestWorkspaceHardening_AllToolsAllowInWorkspace` | all | positive control — in-workspace paths are allowed |

### `internal/shell/environment_hardening_test.go`

Validates shell-level security: command deny list, environment filtering, and sandbox markers.

| Test | Security Layer | What It Verifies |
|------|----------------|------------------|
| `TestEnvironmentHardening_CommandBlockingViaBlockFuncs` | Deny list | Banned commands (curl, wget, ssh, sudo) return "not allowed for security reasons" |
| `TestEnvironmentHardening_ArgumentsBlocker` | Argument blockers | Package managers with install flags blocked (npm -g, pip --user, python -c, node -e) |
| `TestEnvironmentHardening_SelfExecBlocker` | Self-execution | go run ., go build ., phosphor are blocked |
| `TestEnvironmentHardening_SandboxMarkersPresent` | Markers | PHOSPHOR=1, PHOSPHOR_AGENT=true, AGENT=phosphor, AI_AGENT=phosphor present in default shell |
| `TestEnvironmentHardening_SandboxMarkersWithExplicitEnv` | Markers | Markers appended even when caller provides explicit Env slice |
| `TestEnvironmentHardening_FilterEnvWithAllowedEnv` | Env filtering | AWS_SECRET_ACCESS_KEY invisible when AllowedEnv=[PATH,HOME]; PATH visible |
| `TestEnvironmentHardening_EmptyAllowedEnv_FiltersEverything` | Env filtering | Empty allowlist filters ALL environment variables including PATH |

### `internal/agent/tools/compositional_chain_test.go`

Validates that chaining individually allowed actions cannot produce forbidden outcomes.

| Test | What It Verifies |
|------|------------------|
| `TestCompositionalChains_PermissionRequiredForChains` | Chained commands (ls && echo) require user permission — cannot auto-execute |
| `TestCompositionalChains_PathTraversalInChain` | Path traversal in chain arguments blocked (cat ../etc/passwd && echo) |
| `TestCompositionalChains_WorkingDirEscapeInChain` | Out-of-workspace working_dir blocked even in chains |
| `TestCompositionalChains_ChainTriggersPermission` | Plain `ls` does NOT trigger permission; `ls && echo` DOES trigger permission |

### `internal/agent/tools/agentjacking_test.go`

Validates that data from "trusted" sources (MCP servers, context files) cannot be used to smuggle malicious instructions or bypass security.

| Test | What It Verifies |
|------|------------------|
| `TestAgentjacking_MCPContentAsPath` | MCP-sourced paths like `../etc/passwd` still blocked by write tool bounds check |
| `TestAgentjacking_MCPContentAsCommand` | MCP-sourced commands (curl, wget, ssh, sudo) blocked by deny list |
| `TestAgentjacking_MaliciousMCPResourceContent` | File containing "ignore security rules" instructions cannot be used to write outside workspace |
| `TestAgentjacking_ZerWidthCharsInPath` | Zero-width characters (\u200B, \uFEFF, \u2060) in paths do not bypass bounds checks |
| `TestAgentjacking_HTMLCommentsInPath` | HTML comments (`<!-- SYSTEM: ... -->`) in paths do not bypass bounds checks |

### `internal/agent/tools/path_obfuscation_test.go`

Validates that IsInside and SmartJoin handle complex path resolutions robustly.

| Test | What It Verifies |
|------|------------------|
| `TestPathObfuscation_ComplexTraversal` | Complex traversals blocked: `./../etc/passwd`, `../../../etc/passwd`, `..\..\etc\passwd`, `../etc/`, `./etc/passwd` |
| `TestPathObfuscation_EncodedPaths` | URL-encoded paths handled (percent-encoding does not bypass checks) |
| `TestPathObfuscation_SymlinkTraversal` | Symlinks pointing outside workspace resolved by EvalSymlinks and blocked |
| `TestPathObfuscation_AbsolutePathVariants` | Absolute paths on all platforms blocked: Windows (`C:\Windows\...`), Linux (`/etc/passwd`), macOS (`/Users/...`) |

### `internal/agent/tools/web_fetch_test.go`

Validates network egress hardening: the `securityTransport` interceptor, IP-block policy, CIDR allow list, localhost exceptions, and TUI permission prompt.

| Test | What It Verifies |
|------|------------------|
| `TestWebFetchHardeningIPBlock` | Raw IP addresses (e.g. `192.168.1.1`) rejected — FQDN required |
| `TestWebFetchHardeningCIDRAllow` | CIDR allow list (`192.168.1.0/24`) respected — IP falls in range allowed |
| `TestWebFetchHardeningLocalhostAlwaysAllowed` | `127.0.0.1` / `::1` always allowed — no config needed |
| `TestWebFetchHardeningUserPromptVerified` | Unknown host triggers TUI prompt — user granted access |
| `TestWebFetchHardeningUserPromptDenyVerified` | Unknown host triggers TUI prompt — user denied access → request blocked |

### `internal/agent/tools/memory_poisoning_test.go`

Validates that persistent state (context files like AGENTS.md, PHOSPHOR.md) cannot override hardcoded security policies.

| Test | What It Verifies |
|------|------------------|
| `TestMemoryPoisoning_ContextFileInjection` | Malicious AGENTS.md ("ignore security rules") does not allow out-of-workspace writes |
| `TestMemoryPoisoning_ZeroWidthInContext` | Zero-width characters in PHOSPHOR.md do not bypass security |
| `TestMemoryPoisoning_HTMLInContext` | HTML comments in CLAUDE.md do not bypass security |
| `TestMemoryPoisoning_BashToolSecurity` | Bash tool still blocks banned commands and out-of-workspace working_dir even with malicious GEMINI.md |
| `TestMemoryPoisoning_EditToolSecurity` | Edit tool still blocks out-of-workspace edits even with malicious AGENTS.md |

### `internal/agent/tools/fuzzing_test.go`

Validates that all security guards **fail-closed** (reject input) rather than **fail-open** (panic/crash). A crash in a security check is itself a vulnerability — if `IsInside` panics, the caller may silently proceed. These tests feed random garbage into every guard and assert `NotPanics`, ensuring robustness under adversarial or malformed input.

| Test | What It Verifies |
|------|------------------|
| `TestFuzzing_IsInsideHandlesGarbagePaths` | 100 random garbage paths fed into `filepathext.IsInside` — never panics |
| `TestFuzzing_IsInsideHandlesEmptyAndSpecialPaths` | Null bytes, empty strings, control chars, very long dot-strings — never panics |
| `TestFuzzing_BlockFuncHandlesGarbageArgs` | 100 random garbage commands fed into `shell.Exec` with block funcs — never panics |
| `TestFuzzing_BlockFuncHandlesEmptyAndSpecialCommands` | Null bytes, whitespace-only, very long strings through shell Exec — never panics |
| `TestFuzzing_WriteToolHandlesGarbagePaths` | 50 random garbage paths through full write tool pipeline (SmartJoin → IsInside) — never panics |

## Running the Tests

```bash
# Individual test suites
go test ./internal/agent/tools/ -run 'TestWorkspaceHardening' -count=1
go test ./internal/shell/ -run 'TestEnvironmentHardening' -count=1
go test ./internal/agent/tools/ -run 'TestCompositionalChains' -count=1
go test ./internal/agent/tools/ -run 'TestAgentjacking' -count=1
go test ./internal/agent/tools/ -run 'TestPathObfuscation' -count=1
go test ./internal/agent/tools/ -run 'TestMemoryPoisoning' -count=1
go test ./internal/agent/tools/ -run 'TestFuzzing' -count=1

# All new security tests together
go test ./internal/agent/tools/ ./internal/shell/ \
  -run 'TestWorkspaceHardening|TestEnvironmentHardening|TestCompositionalChains|TestAgentjacking|TestPathObfuscation|TestMemoryPoisoning|TestFuzzing' \
  -count=1
```

## Test Statistics

| File | Tests | Coverage Area |
|------|-------|---------------|
| `workspace_hardening_test.go` | 12 | Per-tool bounds checks (10 tools + positive control) |
| `environment_hardening_test.go` | 7 | Command deny list, env filtering, sandbox markers |
| `compositional_chain_test.go` | 4 | Chained command security |
| `agentjacking_test.go` | 5 | MCP injection, hidden characters, HTML comments |
| `path_obfuscation_test.go` | 4 | Complex paths, encoding, symlinks, absolute paths |
| `memory_poisoning_test.go` | 5 | Context file injection, persistent state poisoning |
| `fuzzing_test.go` | 5 | Fail-closed verification (random garbage → no panics) |
| **Total** | **42** | Full security regression coverage |

## Maintenance Notes

- All tests use `t.Parallel()` for fast execution.
- Tests that require `bash` on non-Windows platforms skip on Windows (`runtime.GOOS == "windows"`).
- Error message assertions use substring matching (`Contains`) to catch wording regressions without being fragile.
- The `securityViolationMsg` constant (`"Security violation: path"`) is shared across workspace hardening tests.
- When adding new tools, follow the existing pattern: resolve path with `SmartJoin`, call `IsInside`, return `fantasy.NewTextErrorResponse` on failure.
- When adding new banned commands, update both `internal/agent/tools/bash.go:bannedCommands` and the relevant environment hardening tests.
- **Fail-closed principle**: Security guards must never panic. Fuzzing tests (`TestFuzzing_*`) use `require.NotPanics()` to verify that garbage input is rejected (returns error/false) rather than crashing. A crash in a security check is a fail-open vulnerability.
