# Context Files & Data Directory

## Context Paths

Phosphor injects selected markdown files into every prompt as project
context. The defaults (`AGENTS.md`, `PHOSPHOR.md`, `CLAUDE.md`, `GEMINI.md`,
and their `.local` variants) are always picked up from the working directory.
Add anything else via `options.context_paths`:

```json
{
  "options": {
    "context_paths": ["AGENTS.md", "PHOSPHOR.md", ".cursorrules"],
    "global_context_paths": ["~/.config/phosphor/PHOSPHOR.md"]
  }
}
```

- `context_paths` — files relative to the workspace root, injected per project.
- `global_context_paths` — files injected regardless of workspace (useful for
  personal coding standards or team-wide instructions).

## Skills Paths

Skills are discovered from several built-in directories. Override or extend
with `options.skills_paths`:

```json
{
  "options": {
    "skills_paths": ["~/.config/phosphor/skills", "./skills"]
  }
}
```

Disabling specific skills:

```json
{
  "options": {
    "disabled_skills": ["phosphor-config"]
  }
}
```

## Data Directory

The data directory holds the SQLite session database, logs, and the
workspace-scoped `phosphor.json`:

```json
{
  "options": {
    "data_directory": ".phosphor"
  }
}
```

Relative paths resolve against the working directory. You can also set it per
invocation with `--data-dir`.

## Initialize As

When you run `phosphor init`, it creates a project context file. Change the
default filename:

```json
{
  "options": {
    "initialize_as": "AGENTS.md"
  }
}
```

## Profiles

A profile is a named collection of **system-prompt partial overrides**. It
changes how the agent behaves (tone, autonomy thresholds, decision heuristics)
without touching your code or provider setup.

### Selecting the active profile

The active profile defaults to `"default"`. Override it with the `--profile`
CLI flag (`phosphor --profile fiduciary`) or by setting
`options.agent.active_profile` in any `phosphor.json` file — the global config
(`~/.config/phosphor/phosphor.json`), a project config, or the workspace config
(`.phosphor/phosphor.json`) all work and merge in the usual order:

```json
{
  "options": {
    "agent": {
      "active_profile": "fiduciary"
    }
  }
}
```

### Where profile files go

Each profile lives in a directory named after the profile, and is resolved
from three layers — the first file found wins:

```
1. Workspace → <workspace>/.phosphor/profiles/<profile>/<partial>.md.tpl
2. Global    → ~/.phosphor/profiles/<profile>/<partial>.md.tpl
3. Embedded  → (bundled default, no file required)
```

Note the two live in different roots: the workspace profile dir is inside the
project's `.phosphor/` data directory, while the global profile dir is under
your home directory (`~/.phosphor/`, *not* `~/.config/phosphor/`).

A workspace profile overrides the same-named global profile, which in turn
overrides the built-in default. You only place the partials you want to
change; anything missing cascades to the next layer.

```
~/.phosphor/profiles/fiduciary/
├── rules.md.tpl
├── style.md.tpl
└── decision_making.md.tpl      # others fall back to embedded defaults
```

### Available partials

| Partial | Section tag | Controls |
|---|---|---|
| `rules.md.tpl` | `<critical_rules>` | Hard operational rules |
| `style.md.tpl` | `<communication_style>` | Response tone and formatting |
| `workflow.md.tpl` | `<workflow>` | Task-execution methodology |
| `interaction_gating.md.tpl` | `<interaction_gating>` | Autonomy vs. planning gate |
| `decision_making.md.tpl` | `<decision_making>` | Autonomous decision heuristics |
| `coding.md.tpl` | `<coding_protocol>` | Code style and conventions |
| `summarization.md.tpl` | `<compaction>` | Conversation compaction behavior |

Each partial is a Go template (`.md.tpl`) and may use directives like
`{{if .Var}}`. If a partial contains the literal string `# DISABLE_SECTION`,
that whole section is dropped from the system prompt.

### The `fiduciary` profile

`fiduciary` is special: beyond template overrides, selecting it forces
mandatory self-critique reflection after each LLM response. It is the
strictest built-in profile.

For the full prompt-assembly and data-viewing model, see
[SYSTEM_PROMPT.md](../SYSTEM_PROMPT.md).
