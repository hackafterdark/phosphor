# The `/learn` Slash Command

The `/learn` slash command is a TUI-native capability in Phosphor designed to turn reference materials, directories of source code, or online documentation into high-quality, reusable **Agent Skills** dynamically.

Instead of manually drafting a `SKILL.md` file, you can point `/learn` at any resource, and the agent will use its file, directory, and web-fetching tools to research, design, and author the skill for you.

---

## 1. How to Use `/learn`

To teach the agent a new skill, type `/learn` at the beginning of your prompt, followed by a URL, local path, or a description of what you want the agent to learn:

```bash
# Point to online documentation
/learn https://go.dev/doc/effective_go Summarize the core tenets of concurrency

# Point to a local directory or codebase
/learn the REST client in ~/projects/acme-sdk, focus on auth and pagination

# Point to a specific local file or description
/learn how we set up our database connection and connection pools in internal/db/db.go
```

---

## 2. Hybrid Architecture

The `/learn` command is implemented as a **hybrid slash command**. While standard slash commands (like `/menu` or `/stats`) are executed entirely locally in the TUI, a hybrid command intercepts local input and coordinates with the LLM backend:

```
                  ┌──────────────────────────────┐
                  │   User types: /learn <src>   │
                  └──────────────┬───────────────┘
                                 │
                                 ▼
                  ┌──────────────────────────────┐
                  │    TUI Intercepts command    │
                  │   - Sanitizes TUI input state│
                  │   - Exits slash command mode │
                  └──────────────┬───────────────┘
                                 │
                                 ▼
                  ┌──────────────────────────────┐
                  │  Translates to plain English │
                  │  ecosystem-ready instruction  │
                  └──────────────┬───────────────┘
                                 │
                                 ▼
                  ┌──────────────────────────────┐
                  │   Submits to LLM Agent as    │
                  │   a normal conversation turn │
                  └──────────────────────────────┘
```

Because the TUI translates the slash command into a standard plain-English prompt, it naturally bypasses the backend's slash-prevention failsafes, allowing the agent to use its workspace tools (like `read_file`, `write_to_file`, or web searching) to complete the request.

---

## 3. Generated Skill Standard

To remain fully compatible with wider agent ecosystem standards, the `/learn` command instructs the agent to write a `SKILL.md` file adhering to a rich metadata template:

```markdown
---
name: [kebab-case-folder-name-here]
description: [Concise one-liner <= 60 chars: Use when X occurs to achieve Y]
category: [coding | testing | operations | design]
version: 1.0.0
author: Phosphor Agent
---

## When to use
[Describe the specific symptoms, triggers, or user intents that should trigger this skill.]

## Step-by-step procedures
1. [Step 1]
2. [Step 2]
3. [Step 3]

## Examples
* **Prompt:** "[Example prompt triggering this skill]"
* **Expected Result:** [What the agent should produce/do]

## Reference Assets
[List any local scripts, templates, or files the agent should view or execute to perform this skill. If none exist, specify "None."]
```

The completed skill is saved to `.agents/skills/<name>/SKILL.md`.

---

## 4. Loading & Verification

1. **Lazy Loading:** Once authored and saved to `.agents/skills/`, the new skill is **automatically discovered** on startup when you start a **new session**.
2. **System Prompt Inclusion:** The name and description are added to the `<available_skills>` catalog in the system prompt.
3. **Triggering:** If you ask the agent a task matching the skill's description in a future session, the agent will dynamically call `view_file` on `.agents/skills/<name>/SKILL.md` to load the full instructions on-demand!
