

## Prompt Injection Analysis

Prompt injection is a fundamentally different threat model from path traversal. Here the "attack" is **user-controlled content in workspace files being fed directly into LLM system prompts**. Let me map the full attack surface.

### Attack Surface: Where User Content Enters Prompts

| Injection Vector | Where | How Content Flows | Risk Level |
|-----------------|-------|-------------------|------------|
| **Context files** (`AGENTS.md`, `PHOSPHOR.md`, `CLAUDE.md`, etc.) | [`prompt/prompt.go:109-134`](internal/agent/prompt/prompt.go:109) | `os.ReadFile` → `ContextFile.Content` → injected into system prompt | **HIGH** |
| **Config `context_paths`** | [`prompt/prompt.go:157`](internal/agent/prompt/prompt.go:157) | User config specifies arbitrary file paths → full content read and injected | **HIGH** |
| **Skill files** (`SKILL.md`) | [`prompt/prompt.go:171-198`](internal/agent/prompt/prompt.go:171) | `skills.Discover()` → `skills.ToPromptXML()` → injected | **MEDIUM** |
| **Git status** (branch, commits, diff) | [`prompt/prompt.go:231-280`](internal/agent/prompt/prompt.go:231) | `git log`, `git status`, `git branch` output → injected | **LOW** |
| **LSP events** | [`app/lsp_events.go`](internal/app/lsp_events.go) | Diagnostics, completions → sent to LLM as tool results | **LOW** |
| **User messages** (chat input) | TUI → agent → LLM | Direct user input | **N/A** (intentional) |
| **Tool output** (bash, grep, view, etc.) | All tool implementations | Command output → fed back to LLM | **MEDIUM** |

### How Content Flows (Code Trace)

```
User workspace files
├── AGENTS.md, PHOSPHOR.md, CLAUDE.md, etc. (defaultContextPaths)
├── config.Options.ContextPaths (user-configured)
├── SKILL.md files (from skills.Discover)
└── Git metadata (branch, status, commits)
        │
        ▼
prompt/prompt.go:processContextPath()  [line 109]
        │
        ▼
processFile() → os.ReadFile(filePath)  [line 98-106]
        │
        ▼
ContextFile{Path, Content} → PromptDat.ContextFiles
        │
        ▼
Go template execution (coder.md.tpl, task.md.tpl)
        │
        ▼
Full system prompt sent to LLM
```

Key code at [`prompt/prompt.go:98-106`](internal/agent/prompt/prompt.go:98):
```go
func processFile(filePath string) *ContextFile {
    content, err := os.ReadFile(filePath)
    if err != nil {
        return nil
    }
    return &ContextFile{
        Path:    filePath,
        Content: string(content),  // ← Raw file content injected into prompt
    }
}
```

### Prompt Injection Threat Model

**This is NOT a vulnerability in the traditional sense.** It's a **design characteristic** of AI assistants that read workspace files. The threat model is:

| Scenario | Attacker | Impact | Mitigation |
|----------|----------|--------|------------|
| **1. Malicious workspace file** | Anyone who can write to your workspace | LLM executes injected instructions (e.g., "ignore all previous instructions", "exfiltrate API keys") | User awareness — only open trusted repos |
| **2. Supply chain attack** | Repo maintainer commits malicious `AGENTS.md` | LLM follows injected instructions when you open the repo | Review `AGENTS.md` before opening a repo |
| **3. Dependency injection** | Package maintainer ships malicious `PHOSPHOR.md` in a library | Same as above | Same as above |
| **4. Git branch injection** | Someone pushes a branch with malicious commit messages | LLM sees commit messages in context | Lower weight for git metadata |
| **5. Tool output poisoning** | Malicious tool output (e.g., `grep` matches in source code) | LLM processes poisoned output | LLM safety filters at provider level |

### Existing Mitigations in This Codebase

1. **No input sanitization**: Files are read verbatim and injected into prompts. This is intentional — the system trusts the user's workspace.
2. **System prompt templates** (`templates/coder.md.tpl`, `templates/task.md.tpl`) provide some structure but don't escape/sanitize injected content.
3. **Context file deduplication** at [`prompt/prompt.go:159`](internal/agent/prompt/prompt.go:159) prevents duplicate injections.
4. **Git metadata is limited**: `head -20` for status, `-n 3` for commits.

### Risk Acceptance Framework for Prompt Injection

| Question | Answer | Action |
|----------|--------|--------|
| Who controls the workspace? | The user themselves | **Risk accept** — user should only open trusted repos |
| Can an external attacker inject content? | Only if they can write to the user's workspace (same as any file-based attack) | **Risk accept** — requires workspace access |
| Can the LLM provider see the content? | Yes, all content (including injected files) is sent to the LLM provider | **Risk accept** — same as any user message |
| Can the LLM be tricked into harmful behavior? | Potentially, via injected instructions in workspace files | **Risk accept** — this is the intended design; users control their workspace |

### Recommendations

**For risk acceptance documentation:**

1. **Document the threat model**: Prompt injection is a feature, not a bug. The system is designed to give the LLM full context of the workspace. Users are expected to only open trusted repositories.

2. **Add user-facing documentation**: In the README or docs, note that workspace files (AGENTS.md, PHOSPHOR.md, etc.) are read and injected into LLM prompts, and users should review these files before opening untrusted repositories.

3. **No code changes needed** for the current threat model. The "attack" requires the attacker to already have write access to the user's workspace, which is equivalent to the attacker already being able to modify any source file.

**If you want to add prompt injection defenses** (for high-security environments):

```go
// In prompt/prompt.go, you could add content filtering:
func processFile(filePath string) *ContextFile {
    content, err := os.ReadFile(filePath)
    if err != nil {
        return nil
    }
    // Optional: sanitize content to remove potential injection patterns
    sanitized := sanitizeForPrompt(string(content))
    return &ContextFile{
        Path:    filePath,
        Content: sanitized,
    }
}

func sanitizeForPrompt(content string) string {
    // Remove or escape known injection patterns
    // This is a blunt instrument and may break legitimate content
    // Only use in multi-tenant or untrusted-workspace scenarios
    return content
}
```

But this is **not recommended** for the current use case because:
- It degrades the quality of context provided to the LLM
- It creates a false sense of security (prompt injection is never fully preventable)
- The workspace trust model already addresses the threat

### Summary

| Category | Count | Action |
|----------|-------|--------|
| Context files injected into prompts | ~10 files (AGENTS.md, PHOSPHOR.md, etc.) | **Risk accept** — user-controlled workspace |
| Config-specified context paths | User-configured | **Risk accept** — user controls their own config |
| Skill file content | ~5 skills | **Risk accept** — user-installed skills |
| Git metadata | 3 queries (branch, status, commits) | **Risk accept** — low-value injection target |
| Tool output | ~20 tools | **Risk accept** — inherent to AI assistant design |

**Zero prompt injection vectors are exploitable by an external attacker without workspace access.** The threat model is the user's own AI assistant operating on the user's own files. This is by design.