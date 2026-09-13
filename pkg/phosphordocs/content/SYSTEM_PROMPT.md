# System Prompt Engine

The Phosphor system prompt is a **Policy-as-Code** engine built on Go templates. It composes a master prompt from modular, profile-driven partials, with embedded safety defaults as the fallback baseline.

## Architecture Overview

```
┌─────────────────────────────────────────────┐
│  system.md.tpl (master template)            │
│  ┌───────────┐ ┌──────────┐ ┌────────────┐  │
│  │ Critical  │ │ Workflow │ │ Interaction│  │
│  │ Rules     │ │          │ │ Gating     │  │
│  │           │ │          │ │            │  │
│  │ Comm.     │ │ Decision │ │            │  │
│  │ Style     │ │ Making   │ │            │  │
│  └─────┬─────┘ └────┬─────┘ └─────┬──────┘  │
│        │            │             │         │
│        ▼           ▼             ▼         │
│  resolveProfileTemplate() per section       │
│        │            │             │         │
│        ▼           ▼             ▼         │
│  Go-template execution with PromptDat       │
│        │            │             │         │
│        ▼           ▼             ▼         │
│  # DISABLE_SECTION filter                   │
│        │            │             │         │
│        ▼           ▼             ▼         │
│  Inject into master template → final prompt │
└─────────────────────────────────────────────┘
```

## Modular Template Partials

The system prompt is split into **five governance sections**, each defined as a separate `.md.tpl` partial. Every section is independently overridable via profiles.

| Partial File | Section Tag | Purpose |
|---|---|---|
| `rules.md.tpl` | `<critical_rules>` | Hard-coded operational rules (edit protocol, autonomy, security constraints) |
| `style.md.tpl` | `<communication_style>` | Response formatting conventions (conciseness, language, tone) |
| `workflow.md.tpl` | `<workflow>` | Task execution methodology (before/while/before finishing phases) |
| `interaction_gating.md.tpl` | `<interaction_gating>` | Autonomy vs. planning gate thresholds |
| `decision_making.md.tpl` | `<decision_making>` | Autonomous decision-making heuristics |

Each partial is a **Go template** (`.md.tpl`), meaning it can use `{{if .Var}}`, `{{range .Items}}`, and other Go template directives for conditional logic. The embedded defaults are located in `internal/agent/prompt/templates/`.

## Profile System

### How Profiles Work

A profile is a named collection of template partial overrides. The active profile is resolved via the `active_profile` field in `phosphor.json`:

```json
{
  "options": {
    "agent": {
      "active_profile": "fiduciary"
    }
  }
}
```

If omitted, the default profile name is `"default"`.

### Profile Resolution Order (Cascading)

When resolving a partial, Phosphor checks three layers in priority order:

```
1. Workspace override  →  <workspace>/.phosphor/profiles/<profile>/<partial>.md.tpl
2. Global override     →  ~/.phosphor/profiles/<profile>/<partial>.md.tpl
3. Embedded default    →  (bundled in the binary)
```

The first file found wins. If no profile partial exists at either level, the embedded default is used.

### Directory Structure

**Global profiles** (user-wide, in home directory):

```
~/.phosphor/profiles/
├── default/
│   ├── rules.md.tpl
│   ├── style.md.tpl
│   ├── workflow.md.tpl
│   ├── interaction_gating.md.tpl
│   └── decision_making.md.tpl
├── fiduciary/
│   ├── rules.md.tpl
│   ├── style.md.tpl
│   ├── workflow.md.tpl
│   ├── interaction_gating.md.tpl
│   └── decision_making.md.tpl
└── experimental/
    ├── rules.md.tpl
    ├── style.md.tpl
    ├── workflow.md.tpl
    └── ... (any subset — missing partials fall back)
```

**Workspace profiles** (project-specific, in `.phosphor/profiles/`):

```
<workspace>/.phosphor/profiles/
├── default/
│   └── style.md.tpl          ← overrides global default style only
└── project-specific/
    ├── rules.md.tpl           ← workspace-specific rules
    └── workflow.md.tpl        ← workspace-specific workflow
```

### Merge Behavior

Profiles use a **partial override** model — you do not need to copy every partial into each profile directory. Only the sections you want to change need to be present. Missing files automatically cascade to the next resolution layer (workspace → global → embedded default).

For example, if `~/.phosphor/profiles/experimental/rules.md.tpl` does not exist but `~/.phosphor/profiles/default/rules.md.tpl` does, the default rules are used for the experimental profile.

## The Circuit Breaker Pattern

The rendering pipeline follows a strict fail-fast model:

```
1. RESOLVE  → resolveProfileTemplate() picks the file (workspace → global → embedded)
2. PARSE    → Go template.Parse() on the resolved content
3. EXECUTE  → template.Execute() with PromptDat
4. FILTER   → Trim whitespace, check for # DISABLE_SECTION magic word
5. ASSEMBLE → Inject into master system.md.tpl or task.md.tpl
```

If any step fails (file read error, template parse error, execution error), `Build()` halts and agent initialization is blocked — preventing un-governed execution.

## Magic Word: `# DISABLE_SECTION`

A profile partial may contain the literal string `# DISABLE_SECTION` to **prune that entire section** from the system prompt. When detected after rendering, the section is replaced with an empty string, and the corresponding block in the master template is omitted entirely (via `{{if .Var}}` guards).

This allows users to selectively disable governance sections without removing them from the embedded defaults or maintaining full copies across profiles.

Example — disabling the decision-making section:

```md
# DISABLE_SECTION
```

## Special Profiles

### `fiduciary` Profile

The `fiduciary` profile has a built-in behavioral effect beyond template resolution: when active, it forces `EnableReflection = true` on the agent config. This means the agent performs mandatory self-critique reflection after each LLM response, reviewing its output against critical rules before yielding the final answer.

This is implemented in `internal/agent/prompt/prompt.go`:

```go
if store.ActiveProfile() == "fiduciary" {
    cp := *agentCfg
    cp.EnableReflection = true
    agentCfg = &cp
}
```

## Least-Privilege View Structs

To enforce defense-in-depth, each profile partial receives only the data it needs — not the full `PromptDat` struct. This prevents accidental or malicious exposure of sensitive configuration (provider metadata, internal paths, disabled tools) through profile partial templates.

| Partial | View Struct | Exposed Fields |
|---|---|---|
| `rules.md.tpl` | `RulesView` | `.PromptToolCalls`, `.IsGitRepo`, `.Platform`, `.StructuralSearchAvailable`, `.SemanticSearchAvailable`, `.WorkspaceSearchAvailable` |
| `style.md.tpl` | `StyleView` | `.Provider`, `.Model`, `.Platform`, `.Date` |
| `workflow.md.tpl` | `WorkflowView` | `.WorkingDir`, `.IsGitRepo`, `.GitStatus`, `.Platform` |
| `interaction_gating.md.tpl` | `InteractionGatingView` | `.MaxTurns` |
| `decision_making.md.tpl` | `DecisionMakingView` | `.Provider`, `.Model` |

The full `PromptDat` (including `.Config`, `.Providers`, `.ContextFiles`, etc.) is only passed to the master templates (`system.md.tpl` / `task.md.tpl`) during final assembly — never to individual partials.

> **Implication for profile authors**: If you write a custom partial that references `.Config`, `.Providers`, `.GitStatus`, or any field not listed in the table above, the Go template engine will return an error at render time. This is intentional — it surfaces misconfigured partials early rather than silently falling back to defaults.

**Runtime behavior on unknown variables:** The app does not crash. `Build()` returns the error cleanly:
- During **agent startup / model switch**, the error prevents the agent from initializing with that configuration (user sees an error message).
- During **reload** (e.g., config change mid-session), the error is logged and the previous system prompt remains active.
- During **initialization**, the error propagates to the CLI caller.

## Template Data (PromptDat)

Each partial receives a `PromptDat` struct during execution, providing runtime context. These variables are available inside any profile partial via Go template syntax (e.g., `{{.Field}}`).

### Top-Level Fields

| Variable | Type | Description |
|---|---|---|
| `.Provider` | string | LLM provider name (e.g. `"anthropic"`, `"openai"`) |
| `.Model` | string | Model identifier (e.g. `"claude-sonnet-4-20250514"`) |
| `.PromptToolCalls` | bool | `true` if the provider uses XML-style tool call format (e.g. Anthropic). Useful for conditional rules like "issue one tool call at a time." |
| `.WorkingDir` | string | Current working directory (forward slashes) |
| `.IsGitRepo` | bool | `true` if the working directory is a git repository |
| `.Platform` | string | OS platform (`GOOS`: `"windows"`, `"darwin"`, `"linux"`, etc.) |
| `.Date` | string | Current date in `1/2/2006` format (e.g. `"7/1/2026"`) |
| `.GitStatus` | string | Git context: current branch + short status + recent commits. Only set when `IsGitRepo` is true; empty otherwise. |
| `.AvailSkillXML` | string | XML-formatted list of available skills (builtin + user). Empty if no skills are loaded. |
| `.StructuralSearchAvailable` | bool | `true` if tree-sitter structural search is available |
| `.SemanticSearchAvailable` | bool | `true` if codebase indexing is enabled (semantic search available) |
| `.WorkspaceSearchAvailable` | bool | `true` if FTS5 fulltext workspace search is enabled in config |
| `.ContextFiles` | []ContextFile | Workspace-level context files (from `options.context_paths`). Each has `.Path` and `.Content`. |
| `.GlobalContextFiles` | []ContextFile | Global context files (from `options.global_context_paths`). Same structure. |

### Nested: `.Config`

The full `config.Config` struct is exposed. Key sub-fields accessible in templates:

| Variable | Type | Description |
|---|---|---|
| `.Config.Options.ContextPaths` | []string | Configured workspace context file paths |
| `.Config.Options.GlobalContextPaths` | []string | Configured global context file paths |
| `.Config.Options.SkillsPaths` | []string | Configured skill discovery paths |
| `.Config.Options.DisabledSkills` | []string | Disabled skill names |
| `.Config.Options.DisabledTools` | []string | Disabled tool names |
| `.Config.Options.DataDirectory` | string | Application data directory (e.g. `.phosphor`) |
| `.Config.Options.InitializeAs` | string | Name of the context file to create on init |
| `.Config.Providers` | map[string]ProviderConfig | Provider configurations keyed by name |

### Nested: `.Config.Providers.<name>`

When accessing a specific provider (e.g. via `range` or known names):

| Variable | Type | Description |
|---|---|---|
| `.ToolCallFormat` | string | Tool call format (`xml`, `json`, etc.) |
| `.BaseURL` | string | Provider API base URL |
| `.Name` | string | Display name |
| `.ID` | string | Provider identifier |
| `.Disable` | bool | Whether the provider is disabled |

### Nested: `.AgentConfig`

| Variable | Type | Description |
|---|---|---|
| `.AgentConfig.ActiveProfile` | string | Name of the active profile |
| `.AgentConfig.EnableReflection` | bool | Whether self-critique reflection is enabled (always true for `fiduciary` profile) |
| `.AgentConfig.MaxTurns` | int | Max tool-use turns per prompt (0 = unlimited) |

### Example Usage in a Partial

```md
{{- if .PromptToolCalls}}
Issue exactly one tool call per response.
{{- end}}

{{- if .IsGitRepo}}
Working in: {{.WorkingDir}} ({{.Platform}})
{{- end}}
```

> **Available variables depend on which partial you are editing.** See the View Struct table above for the field map per section.

---

## Sub-Agent (Task) Prompt Architecture (`task.md.tpl`)

While the primary agent uses `system.md.tpl` to handle user-facing conversation and multi-stage code modifications, Phosphor runs **sub-agents** under the hood via the parallel `agent` tool for background context gathering and codebase searches.

To optimize token efficiency and prevent sub-agents from executing un-applicable logic, their prompt architecture is highly streamlined.

### Key Differences from `system.md.tpl`

1. **No Interactive/Gating Sections**: 
   * Excludes `interaction_gating` and `skills_usage` entirely. Sub-agents run programmatically and autonomously in parallel threads and do not trigger interactive approvals or modular skill scripts.
2. **No Coding/Planning Sections**: 
   * Excludes `decision_making` and `coding_protocol`. Because sub-agent sessions have read-only permissions (restricted to read-only tools like `grep`, `glob`, `view`, and `structural_search`), instructions on writing code or weighing complex architectural trade-offs are omitted.
3. **Strict Formatting Rules**: 
   * Sub-agents bypass the profile-based communication style rules and are bound to a strict, hardcoded formatting protocol designed for CLI output. This instructs them to deliver concise, factual, direct, and ideally single-word/snippet responses (avoiding conversational filler).
4. **Mandatory Tool Funnel**:
   * Specifically instructs the sub-agent to prioritize `structural_search` (AST-based codebase querying using tree-sitter) before falling back to unstructured text `grep` or file listings.

### Governance and Parity Retained
Despite being lightweight, the sub-agent prompt still inherits critical safety rules and project-specific contexts:
* **Fiduciary Safety**: Loads the profile-driven `<critical_rules>` to ensure the sub-agent respects security boundaries (e.g. secret leak prevention, path validation).
* **Workspace Context**: Receives the full `.ContextFiles` (`<project_context>`) and `.GlobalContextFiles` (`<user_preferences>`) so it remains aware of project conventions and preferred directory paths when searching.

---

## Configuration Reference

### phosphor.json Agent Block

```jsonc
{
  "options": {
    "agent": {
      // Which profile to load governance partials from.
      // Values: any string; defaults to "default" if omitted.
      // Special values: "fiduciary" (forces reflection on).
      "active_profile": "fiduciary",

      // Toggle self-critique reflection after each LLM response.
      // Automatically set to true when active_profile is "fiduciary".
      "enable_reflection": false,

      // Maximum tool-use turns per prompt (0 = unlimited).
      "max_turns": 0
    }
  }
}
```

### Runtime Overrides

The active profile can also be overridden at runtime via `RuntimeOverrides` in the config store, allowing the TUI to change profiles without editing `phosphor.json`. Runtime overrides take precedence over config file values.

## Implementation Details

- **Package**: `internal/agent/prompt`
- **Key function**: `Prompt.Build()` — orchestrates the full pipeline
- **Resolution function**: `resolveProfileTemplate()` — handles cascading file lookup
- **Embedded defaults**: Go `//go:embed` directives in `prompt.go`
- **Master templates**: `internal/agent/templates/system.md.tpl`, `task.md.tpl`, `initialize.md.tpl`
