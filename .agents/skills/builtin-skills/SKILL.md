---
name: builtin-skills
description:
  Use when creating a new builtin skill for Phosphor, editing an existing builtin
  skill (pkg/skills/builtin/), or when the user needs to understand how the
  embedded skill system works.
---

# Builtin Skills

Phosphor embeds skills directly into the binary via `pkg/skills/builtin/`.
These are always available without user configuration.

## How It Works

- Each skill lives in `pkg/skills/builtin/<skill-name>/SKILL.md`.
- The tree is embedded at compile time via `//go:embed builtin/*` in
  `pkg/skills/embed.go`.
- `DiscoverBuiltin()` walks the embedded FS, parses each `SKILL.md`, and sets
  paths with the `phosphor://skills/` prefix (e.g., `phosphor://skills/jq/SKILL.md`).
- The View tool resolves `phosphor://` paths from the embedded FS, not disk.
- User skills with the same name override builtins (last occurrence wins in
  `Deduplicate()`).

## Adding a New Builtin Skill

1. Create `pkg/skills/builtin/<skill-name>/SKILL.md` with YAML frontmatter
   (`name`, `description`) and markdown instructions. The directory name must
   match the `name` field.
2. No extra wiring needed — `//go:embed builtin/*` picks up new directories
   automatically.
3. Add a test assertion in `TestDiscoverBuiltin` in
   `pkg/skills/skills_test.go` to verify discovery.
4. Build and test: `go build . && go test ./pkg/skills/...`

## Existing Builtin Skills

| Skill          | Directory               | Description                                |
| -------------- | ----------------------- | ------------------------------------------ |
| `phosphor-config` | `builtin/phosphor-config/` | Phosphor configuration help                   |
| `phosphor-hooks`  | `builtin/phosphor-hooks/`  | Authoring, configuring and debugging hooks |
| `phosphor-cron-jobs` | `builtin/phosphor-cron-jobs/` | Authoring scheduled-job `job.md` files  |
| `jq`           | `builtin/jq/`           | jq JSON processor usage guide              |
