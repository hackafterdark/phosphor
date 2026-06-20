# ADR-0003: Append Tool for File Content Addition

## Status

Accepted

## Context

Phosphor's file writing tools (`write`, `edit`, `multiedit`) all operate on the full file content. The `write` tool overwrites a file entirely, while `edit` and `multiedit` require reading the file first to perform find-and-replace operations.

When the agent needs to add content to an existing file — such as appending log entries, adding documentation sections, or extending code files — it has no dedicated tool for this operation. The current workflow is:

1. Read the entire file with `view` (capped at 200KB).
2. Compute the new content (existing + new).
3. Call `write` with the full combined content.

This approach has several problems:

1. **200KB read cap**: The `view` tool's `MaxViewSize = 200KB` limit means the agent cannot read files larger than 200KB, making it impossible to use `write` for appending to large files.
2. **Unnecessary reads**: For simple additions (e.g., appending a log line), reading the entire file is wasteful and slow.
3. **Truncation risk**: `write` replaces the file entirely. If the agent's content is large and the response is truncated (e.g., by the 30KB bash output limit or context window constraints), the file can be partially overwritten with incomplete content.
4. **Awkward workarounds**: Agents resort to running shell commands like `wc -l` to estimate file size, or chaining multiple `write` calls with manual offset tracking — both fragile and error-prone.

The `view` tool already supports sectional reads via `offset` and `limit` parameters, so the agent *can* read large files in chunks. However, there is no corresponding tool to *write* content at a specific position in a file.

## Decision

We added a new `append` tool that uses `os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)` to append content to a file. The tool:

- Creates the file if it doesn't exist (with `os.O_CREATE`).
- Creates parent directories if needed.
- Appends content at the current file end (no offset parameter).
- Includes the same safety checks as `write`: file modification detection via filetracker, permission dialog with diff metadata, file history tracking, and LSP notifications.
- Does **not** automatically add newlines — content is appended exactly as provided, keeping behavior predictable.

The tool is registered in the coordinator alongside `write`, `edit`, and `multiedit`, with full UI support (message rendering, permission dialog, compact content, clipboard formatting).

### Files

- `internal/agent/tools/append.go` — tool implementation
- `internal/agent/tools/append.md` — tool description
- `internal/agent/tools/append_test.go` — tests
- `internal/agent/templates/coder.md.tpl` — tool selection rules and append contract

### System Prompt Rules

The coder system prompt includes three rules to guide tool selection:

1. Always use `write` for creating new files or replacing existing content entirely.
2. Use `append` for adding to existing logs, documentation, or code files to avoid unnecessary file reads and truncation risks.
3. APPEND CONTRACT: When using `append`, the agent is responsible for maintaining file structure and must check the file's ending before appending, explicitly prepending `\n` if needed.

## Consequences

**Positive:**
- Agents can now append to files of any size without reading them first, bypassing the 200KB view cap for this use case.
- Eliminates the need for `wc -l` workarounds and manual offset tracking.
- Reduces truncation risk — `append` writes only the new content, not the full file.
- The append contract in the system prompt gives the agent explicit guidance on newline handling.
- Consistent with existing tool patterns (permission dialog, diff metadata, file history, LSP notifications).

**Negative:**
- Adds one more tool to the agent's toolkit, increasing the decision surface.
- The append contract requires the agent to check file endings, which means an extra `view` call in some cases.
- `append` and `write` have overlapping use cases (e.g., writing to a new file works with both), which could cause confusion. The system prompt rules are intended to disambiguate.
- No offset-based write support — the agent still cannot insert content at an arbitrary position in a file.

**Open Questions:**
- Should we add an `insert` tool that writes at a specific line offset? This would cover the remaining gap for mid-file content insertion.
- Should the `write` tool description be updated to explicitly mention that `append` is preferred for additions to existing files?
- The append contract relies on the agent being disciplined about checking file endings. Could this be enforced more robustly (e.g., a tool-level check that warns when appending without a preceding newline)?

## Alternatives

### Extending `write` with an `append` flag

Instead of a separate tool, we could add an `append` boolean parameter to the existing `write` tool:

```json
{"file_path": "log.txt", "content": "new entry", "append": true}
```

**Why This Was Rejected:**
- The `write` tool already has a well-established contract (overwrite). Adding an `append` mode would split its semantics and make the tool description longer and more complex.
- A separate tool makes the tool selection rules clearer: `write` for overwrite, `append` for add. The system prompt can reference them unambiguously.
- The permission dialog would need to show different diff semantics for append mode (showing what's being added vs. what's being replaced), which is cleaner with a dedicated tool.

### Adding an `insert` tool with line offset

An `insert` tool could write content at a specific line number, filling the gap between `append` (end of file) and `write` (full replacement).

**Why This Was Deferred:**
- `append` covers the most common pain point (large file additions without reads).
- `insert` would require the agent to know the exact line number, which means reading the file first — partially defeating the purpose of avoiding reads.
- Can be added later if a clear need emerges.

### Using shell commands for appending

The agent could use `bash` with `echo "content" >> file` or `printf "content" >> file` to append.

**Why This Was Rejected:**
- Shell-based appending bypasses Phosphor's permission system, file history tracking, and LSP notifications.
- No diff metadata for the permission dialog.
- Shell output is subject to the 30KB truncation limit, making it unreliable for large content.
- No file modification detection (filetracker guard).

## References

- `internal/agent/tools/append.go` — tool implementation
- `internal/agent/tools/append.md` — tool description
- `internal/agent/tools/append_test.go` — tests
- `internal/agent/tools/write.go` — reference implementation (write tool)
- `internal/agent/coordinator.go:643` — tool registration
- `internal/ui/chat/file.go` — UI message rendering
- `internal/ui/dialog/permissions.go` — permission dialog support
- `internal/proto/tools.go` — tool name and type exports
- `internal/agent/templates/coder.md.tpl` — system prompt tool selection rules
