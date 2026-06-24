# Phosphor Skill System Architecture

Skills in Phosphor represent a powerful, light-weight, and extensible mechanism to teach the AI agent specialized workflows, guidelines, and procedures. 

The core lazy, on-demand skill loading architecture in Phosphor was originally designed and implemented by the Charm team in the **Crush** coding assistant. Phosphor inherits and extends this elegant design, adding support for dynamic workspace-scoped custom skills, local generation via `/learn`, and robust user configuration.

---

## 1. Core Architecture: On-Demand (Lazy) Loading

To keep the agent's context window clean, costs low, and reasoning focused, Phosphor does **not** load the full instructions of all available skills at startup. Instead, it utilizes a **lazy, progressive disclosure loading pattern**.

This splits skill loading into three levels:

```
┌────────────────────────────────────────────────────────┐
│                        Level 0                         │
│   System prompt contains only metadata names & paths   │
│   (~3k tokens total for all available skills)          │
└───────────────────────────┬────────────────────────────┘
                            │
                            │ (Agent matches task to description)
                            ▼
┌────────────────────────────────────────────────────────┐
│                        Level 1                         │
│  Agent calls `view_file` on the skill's SKILL.md path  │
│  TUI loads instructions dynamically into chat context  │
└───────────────────────────┬────────────────────────────┘
                            │
                            │ (Agent needs reference assets)
                            ▼
┌────────────────────────────────────────────────────────┐
│                        Level 2                         │
│  Agent calls `view_file` on custom scripts/templates   │
│  within the skill's subdirectory                       │
└────────────────────────────────────────────────────────┘
```

### Level 0: Discovery (The Prompt Catalog)
At the start of every session, the workspace's `skills.Manager` scans all configured skill paths. It reads only the YAML frontmatter (name and description) of each skill. 

This metadata is compiled into a lightweight XML tag in the agent's system prompt:
```xml
<available_skills>
  <skill>
    <name>go-concurrency-patterns</name>
    <description>Idiomatic Go concurrency patterns and best practices.</description>
    <location>.agents/skills/go-concurrency-patterns/SKILL.md</location>
  </skill>
</available_skills>
```

### Level 1: Dynamic Interception (On-Demand Loading)
The system prompt contains a strict instruction directing the agent how to act:
> *“If any entry in `<available_skills>` matches the current task, you MUST call `view` on its `<location>` before taking any other action for that task.”*

When the agent decides a skill is relevant:
1. It issues a standard `view_file` tool call on the skill's `<location>`.
2. The TUI/backend intercepts the tool call.
3. The file contents are read and injected into the active conversation turn.
4. The TUI marks the skill as loaded in the `skills.Tracker`, changing its bullet point to green in the **Skills** sidebar.

### Level 2: Specific Reference Files
Some skills include helper scripts, code templates, or additional assets in their subdirectory. The agent can use its standard `view_file` or bash execution tools to load or run these assets directly as needed.

---

## 2. Skill Types and Locations

Phosphor distinguishes between two origins of skills:

| Type | Path / URI Scheme | Description |
| :--- | :--- | :--- |
| **Builtin Skills** | `phosphor://skills/<name>/SKILL.md` | Pre-compiled skills embedded directly inside the Phosphor binary (under `internal/skills/builtin/`). |
| **Workspace Skills** | `.agents/skills/<name>/SKILL.md` | Local, project-specific skills residing in the current working directory's `.agents/` folder. |

Because all skills are mapped to paths, the agent does not need any specialized skill-loading tools. It simply uses its standard general-purpose **`view_file`** tool, maintaining a minimal and clean tool interface.

---

## 3. Creating & Managing Skills

### Creating Skills with `/learn`
You can author new workspace skills dynamically by using the `/learn` TUI slash command:
```bash
/learn how we set up our database connection in db.go
```
The TUI translates the slash command into a structured instruction prompt. The agent then uses its workspace tools to research the code, write a high-quality `SKILL.md` following project authoring standards, and save it to `.agents/skills/`.

*Note: Newly created skills are automatically discovered and loaded into the system prompt when a **new session** is started.*

### Enabling & Disabling Skills
Skills can be disabled globally or per-workspace by editing the `phosphor.json` configuration file:

```json
{
  "options": {
    "disabled_skills": [
      "computer-use",
      "go-concurrency-patterns"
    ]
  }
}
```

On startup, the discovery engine automatically filters out any disabled skills from the `<available_skills>` list, ensuring they are never shown to the agent.
