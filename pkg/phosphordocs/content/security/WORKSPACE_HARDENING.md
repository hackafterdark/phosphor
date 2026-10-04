# Workspace Filesystem Hardening

## Threat Model

The Phosphor agent runs inside a terminal and is given tools to read, write, search, and execute commands on the filesystem. Without confinement, a prompt-injected or misbehaving agent could:

- Write files outside the project directory (e.g. `C:\Windows\System32\...`).
- Read sensitive files outside the workspace (e.g. `/etc/passwd`, home directory configs).
- Use `bash` to `cd` into or write to arbitrary paths on the filesystem.
- Download URLs to arbitrary locations outside the workspace.
- Search or list directories outside the workspace boundary.

The goal of this hardening is to treat the workspace directory as a **strict security sandbox**. Any operation that resolves to a path outside `workspaceDir` must be blocked immediately with a clear error — never silently allowed via permission prompts.

## Architecture: Five Layers of Defense

```
┌─────────────────────────────────────────────────┐
│  Layer 5: Startup Symlink Resolution            │
│  Workspace root is resolved via EvalSymlinks    │
│  before config initialization to prevent        │
│  bypass through symlink traversal in bounds     │
│  checking.                                      │
├─────────────────────────────────────────────────┤
│  Layer 4: Shell CWD Lockdown (bash escape)      │
│  "cd" commands cannot move the working          │
│  directory outside the workspace root.          │
├─────────────────────────────────────────────────┤
│  Layer 3: Bash working_dir guard                │
│  The --working-dir parameter is validated       │
│  against the workspace before any command       │
│  runs. Absolute paths, relative paths, and      │
│  symlink traversal are all checked.             │
├─────────────────────────────────────────────────┤
│  Layer 2: Per-tool path bounds checks           │
│  Every filesystem-touching tool validates the   │
│  resolved absolute path against the workspace   │
│  before performing any I/O.                     │
└─────────────────────────────────────────────────┘
         │
         ▼
  filepathext.IsInside(absPath, absWorkspace)
  (the single source of truth for all checks)
```

## The Core Helper: `filepathext.IsInside`

**Location:** `internal/filepathext/bounds.go`

```go
func IsInside(absPath, absWorkspace string) bool
```

Returns `false` if either path is empty, if the relative path from workspace to target is `..`, or if it starts with `..` followed by a path separator. Both paths must be absolute before calling this function.

**Symlink resolution:** To prevent bypass through symlink traversal, both `absPath` and `absWorkspace` are resolved via `filepath.EvalSymlinks` before comparison. If a path doesn't exist on disk (e.g., during tests), the check proceeds using the original paths. This ensures that even if a symlink points outside the workspace, the bounds check sees the resolved target.

**Case-insensitive FS compatibility:** On Windows and macOS (case-insensitive filesystems), `filepath.Rel` operates on raw strings and can produce spurious `..` prefixes when the workspace and target paths differ only in case (e.g. `C:\Users\test\Project` vs `C:\Users\test\project`). Both paths are normalized to lowercase via `strings.ToLower` before the `Rel` call to prevent false security violations on these platforms.

**Tests:** `internal/filepathext/bounds_test.go` — covers same-directory, nested files, parent directories, unrelated paths, empty inputs, and case-different path pairs.

All bounds checks across the codebase delegate to this single function. If the logic ever needs adjustment, there is exactly one place to change it.

## Path Joining: `SmartJoin` and `UnsafeSmartJoin`

**Location:** `internal/filepathext/filepath.go`

```go
func SmartJoin(one, two string) string
func UnsafeSmartJoin(one, two string) string
func ValidatePath(absPath, absWorkspace string) error
```

- **`SmartJoin`**: Joins two paths, treating the second path as absolute if it is an absolute path. Does not validate against a workspace — use `ValidatePath` separately for that.
- **`UnsafeSmartJoin`**: Alias for `SmartJoin`, provided for clarity when the caller intends to bypass workspace validation. Only use this for trusted extensions (e.g. MCP servers) that legitimately need cross-workspace access.
- **`ValidatePath`**: Checks whether `absPath` is inside `absWorkspace` and returns an error if it is not. Both paths must be absolute before calling this function.

The distinction between `SmartJoin` and `UnsafeSmartJoin` is intentional: most tools use `SmartJoin` and then validate via `IsInside`, while MCP servers and shell dispatch use `UnsafeSmartJoin` because they operate outside the workspace sandbox by design (see "What Is NOT Constrained" below).

## Layer 1: Per-Tool Bounds Checks

Every tool that touches the filesystem resolves its target path to an absolute path and calls `IsInside` **before** performing any I/O. If the check fails, the tool returns a `Security violation: path <path> is outside workspace` error via `fantasy.NewTextErrorResponse`.

### File-write tools (write, edit, multiedit, append)

| Tool | Location | Behavior |
|------|----------|----------|
| `write.go` | `internal/agent/tools/write.go:74` | After `SmartJoin`, resolves absolute path and checks `IsInside`. Rejects with security error. |
| `edit.go` | `internal/agent/tools/edit.go:85` | Same pattern — validates before any content replacement or file creation. |
| `multiedit.go` | `internal/agent/tools/multiedit.go:88` | Same pattern — validates before any edit operations. |
| `append.go` | `internal/agent/tools/append.go:80` | Previously had its own inline bounds check. Refactored to use `IsInside` for consistency. |

### File-read tools (view, download)

These previously used a **permission-request pattern** — they would detect out-of-workspace paths and ask the user for approval. That pattern is removed; these tools now hard-block instead.

| Tool | Location | Behavior |
|------|----------|----------|
| `view.go` | `internal/agent/tools/view.go:122` | Replaced permission gate with hard block. The old `isSkillFile` workaround (which allowed reading skill files outside the workspace) is removed. Builtin skills via `phosphor://skills/...` prefix are handled separately before the bounds check and remain accessible. |
| `download.go` | `internal/agent/tools/download.go:90` | Added bounds check after `SmartJoin`. Removed unused `cmp` import that was only needed for the old relative-path formatting. |

### Search and listing tools (ls, glob, grep, structural_search)

These previously used permission requests for out-of-workspace directories. All now hard-block.

| Tool | Location | Behavior |
|------|----------|----------|
| `ls.go` | `internal/agent/tools/ls.go:93` | Replaced permission gate with hard block after path resolution. |
| `glob.go` | `internal/agent/tools/glob.go:68` | Bounds check after `ResolveSearchPath`. |
| `grep.go` | `internal/agent/tools/grep.go:146` | Same — bounds check after `ResolveSearchPath`. |
| `structural_search.go` | `internal/agent/tools/structural_search.go:302` | Same — bounds check after `ResolveSearchPath`. |

### Cross-reference tools (references, diagnostics)

These tools accept user-supplied paths and previously had no workspace validation. Both now validate against the workspace before performing operations.

| Tool | Location | Behavior |
|------|----------|----------|
| `references.go` | `internal/agent/tools/references.go:38` | Accepts `workingDir` parameter. Validates `params.Path` against workspace via `IsInside` before calling `searchFiles`. Prevents symbol search in files outside workspace (e.g., `/etc/passwd`). |
| `diagnostics.go` | `internal/agent/tools/diagnostics.go:29` | Accepts `workingDir` parameter. Validates `params.FilePath` against workspace via `IsInside` before triggering LSP analysis. Prevents LSP side-effects on files outside workspace. |

## Layer 2: Bash Working Directory Guard

**Location:** `internal/agent/tools/bash.go:217`

The bash tool accepts an optional `working_dir` parameter. Previously, the agent could pass any absolute path (e.g. `C:\Windows`) as `working_dir` and commands would execute there with no restriction.

Now, before any command runs:

```go
absWorkingDir, _ := filepath.Abs(workingDir)
absExecDir, _ := filepath.Abs(execWorkingDir)
if !filepathext.IsInside(absExecDir, absWorkingDir) {
    return error("Security violation: working directory ... is outside workspace")
}
```

This covers absolute paths, relative paths that resolve outside the workspace, and symlink traversal.

## Layer 3: Shell CWD Lockdown

**Location:** `internal/shell/shell.go:68` (Shell struct), `shell.go:280` (`updateShellFromRunner`)

Even with the working_dir guard, a command like `cd /some/outside/path && ls` could change the shell's internal working directory mid-execution. The POSIX shell interpreter (`mvdan.cc/sh/v3`) tracks `runner.Dir` and updates the Shell's `cwd` after each command via `updateShellFromRunner`.

A new optional `workspace` field on the `Shell` struct and `Options` enforces a hard boundary:

```go
func (s *Shell) updateShellFromRunner(runner *interp.Runner) {
    newCwd := runner.Dir
    if s.workspace != "" {
        rel, err := filepath.Rel(s.workspace, newCwd)
        if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
            newCwd = s.cwd  // reset to prevent escape
        }
    }
    s.cwd = newCwd
    // ... env propagation
}
```

If a `cd` command would move the working directory outside the workspace, it is reverted after the command completes. The agent sees the original cwd in `<cwd>` tags and never carries an escaped cwd into the next command.

The background shell manager passes the workspace root through to each new Shell:

**Location:** `internal/shell/background.go:89` — `Start(ctx, workingDir, workspace, blockFuncs, command, description)`

### Why the post-command reset is not sufficient

The reset in `updateShellFromRunner` runs *after* the interpreter finishes the
command. `cd` is a special builtin dispatched inside `mvdan.cc/sh/v3` and never
reaches the exec-handler middleware chain, so a compound command such as
`cd C:/ && cat Windows/win.ini` or `cd ~ && ls .ssh` executed the second part
with the interpreter's cwd already outside the workspace: the relative operand
resolved against the escaped cwd and read the file, while the post-command
reset silently restored cwd and hid the violation from the transcript.

### Layer 3a: Static `cd` Target Confinement

**Locations:** `internal/pathguard/pathguard.go` (`ValidateCommandPaths`),
`pkg/shell/confinement_ast.go` (`checkProgram`)

The static validator no longer skips `cd` commands wholesale — every path
token in a `cd`-prefixed command (including the `cd` target itself and the
operands of any commands chained after it) is bounds-checked before the shell
starts. Independently, the AST walk in `checkProgram` bounds-checks the
expanded target of every `cd` call node, so an explicit escape (`cd C:/`,
`cd ~`, `cd /etc`) is refused before any I/O happens. Unprovable targets such
as `cd $(mktemp -d)` are tolerated at this layer — exactly like output
redirection operands — because the runtime layer below gates any file access
that follows.

#### Heredocs and herestrings are data, not operands

The raw-text validator must distinguish *operands* (paths the shell opens or
execs) from *data* (bytes streamed to stdin). A here-document body is data:
path-like text inside it — a drive root in a commit message, a `..` in a
patch — must neither trigger a violation nor be rewritten in place by
`CorrectCommandPaths`. `maskHeredocBodies` blanks every heredoc body (and
herestring operand) to spaces before tokenization, preserving byte offsets
and newlines. The mask also repairs quote tracking: without it, a `"`
embedded in heredoc data closes an enclosing quoted argument, fragments the
argument, and exposes the body's words as bare path tokens — the false
positive that once blocked a commit message containing `C:/`. The pre-pass is
deliberately quote-agnostic (any `<<` is treated as an introducer); a
misfire only suppresses the static text scan for those bytes, and the runtime
exec/open handlers remain the authoritative gate, so the bias errs toward
availability, not evasion.

### Layer 3b: Runtime CWD Confinement

**Locations:** `pkg/shell/confinement.go` (`pathConfinementHandler`),
`pkg/shell/run.go` (`pathOpenHandler`), `internal/pathguard/pathguard.go`
(`Confinement.DirBlocked`)

Two runtime gates close the residual window:

1. `DirBlocked` — before any external command executes (exec-handler chain)
   and before any interpreter-opened file (open handler: redirections,
   sourced scripts, here-documents), the interpreter's *current working
   directory itself* is bounds-checked against the trusted roots. A command
   that operates on the cwd without naming a path (`ls`, `du`, `rm -rf .`)
   is refused while cwd sits outside the workspace.
2. `Confinement.Blocked` no longer waves through relative operands while the
   cwd is outside a trusted root. When cwd has escaped, every path-like
   operand is resolved against the real cwd and bounds-checked, so
   `cat Windows/win.ini` after `cd C:/` surfaces a security violation
   instead of reading `C:/Windows/win.ini`.

Together these make a mid-command `cd` escape harmless: the cwd can change,
but nothing that runs or opens files afterwards can act on an outside
directory.

## Layer 4: Agent Prompt Instructions

**Location:** `internal/agent/tools/bash.md.tpl:19`

The bash tool's system prompt now includes an explicit sandbox warning:

> **SANDBOX**: You are running inside a workspace sandbox. Any attempt to access files or directories outside the workspace root will be blocked by the system. The `working_dir` parameter is also constrained to the workspace. Do not try to escape.

This is defense-in-depth — it tells the LLM the rules so it is less likely to attempt evasion, even if a lower layer has a gap.

## Layer 5: Startup Symlink Resolution

**Location:** `internal/config/load.go:51-55`

Before config initialization, the workspace root is resolved via `filepath.EvalSymlinks` to prevent bypass through symlink traversal in bounds checking. If the workspace doesn't exist on disk (e.g., during tests), the original path is used as-is.

```go
if resolvedWorkingDir, err := filepath.EvalSymlinks(workingDir); err == nil {
    workingDir = resolvedWorkingDir
}
```

This ensures that even if the workspace directory itself is a symlink pointing outside the intended bounds, the bounds check sees the resolved target. Combined with the symlink resolution in `IsInside`, this provides defense-in-depth against symlink-based escape attacks.

## What Is NOT Constrained (Intentional Bypasses)

The following components intentionally operate outside the workspace sandbox. These are **not** vulnerabilities — they are design choices based on their role in the system.

| Component | Reason for bypass |
|-----------|-------------------|
| Hook runner (`internal/hooks/`) | Runs user-authored shell commands from `phosphor.json`, not agent-generated ones. The cwd comes from the hook config, not the agent. |
| `getGitStatus` in prompt loading | Internal utility that runs git commands against the project directory. Not agent-facing. |
| LSP client (`internal/lsp/`) | Uses file paths for diagnostics but does not write files. Path filtering already exists (`fsext.HasPrefix`). |
| Config loading | Reads `phosphor.json` and context files (AGENTS.md, etc.) from the working directory. |

### Skills and MCP Servers

Skills and MCP servers are **user-installed extensions** that execute with the agent's privileges. Phosphor cannot sandbox them without breaking their intended functionality — a skill or MCP server that needs to read a global config file would be useless if locked to the workspace. This is an accepted tradeoff: the security boundary covers everything the agent ships with, but user-installed extensions are trusted by definition (the user chose to install them).

If a future feature requires the agent to read configuration or resources outside the workspace (e.g. a global `phosphor.json`), the architecture supports adding an allowlist bypass before the `IsInside` check — similar to how builtin skills accessed via `phosphor://skills/...` remain accessible through a separate code path.

### Trusted Extensions Using `UnsafeSmartJoin`

The following components use `UnsafeSmartJoin` instead of `SmartJoin` because they legitimately need cross-workspace access:

| Component | Location | Reason |
|-----------|----------|--------|
| MCP resource listing | `internal/agent/tools/list_mcp_resources.go:55` | MCP servers may reference resources outside the workspace. |
| MCP resource reading | `internal/agent/tools/read_mcp_resource.go:61` | MCP servers may serve content from arbitrary locations. |
| Shell script dispatch | `internal/shell/dispatch.go:58` | Hook scripts may be located outside the workspace. |

These are explicitly marked with `UnsafeSmartJoin` to make the intentional bypass visible in code review and static analysis.

## Edge Cases and Open Questions

### Builtin skill files

Previously, `view.go` had an `isInSkillsPath` check that allowed reading skill files located outside the workspace via a permission prompt. This is now blocked — any path outside the workspace returns a security error regardless of location.

Builtin skills accessed via the `phosphor://skills/...` prefix are handled by a separate code path (`readBuiltinFile`) before the bounds check runs, so they remain fully accessible.

### Cross-workspace config access

If future features need the agent to read configuration files or other resources outside the workspace (e.g. a global `phosphor.json`), the current architecture supports adding an allowlist. The `IsInside` function and per-tool guards are structured to make this straightforward — add a conditional bypass before the bounds check, similar to how builtin skills are handled.

## Testing

All bounds-check logic is covered by unit tests:

- `internal/filepathext/bounds_test.go` — `IsInside` correctness (7 cases + real-path integration test).
- `internal/agent/tools/append_test.go` — existing append tests pass with the refactored bounds check.
- `internal/shell/background_test.go` — background shell manager tests pass with the updated `Start` signature.
- `internal/pathguard/pathguard_test.go` — `TestCommandEscapesWorkspace_CDCommandValidated` pins that `cd` targets are bounds-checked (absolute, `~`, and compound `cd X && cmd` forms are refused; in-tree `cd` is allowed).
- `internal/pathguard/heredoc_test.go` — `TestHeredocBodyIsData` pins that heredoc bodies, stacked heredocs, `<<-` bodies, and herestring operands are treated as data while real operands on the introducer line are still flagged; `TestHeredocBodyNotRewritten` pins that `CorrectCommandPaths` leaves heredoc data byte-for-byte intact.
- `pkg/shell/confinement_vectors_test.go` — `TestMidCommandCDEscapeBlocked` pins the red-team vectors (`cd <outside> && cat <relative>`, `cd <outside> && ls`, `cd ~ && ls .ssh`, `cd <outside>; echo > file`) are refused; `TestMidCommandCDInTreeAllowed` and `TestCDEscapeHarmlessWithTrustedTempRoots` pin that legitimate in-tree and trusted-temp directory changes still work.
- Full `go test ./internal/...` suite passes with no regressions.
