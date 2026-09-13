# Scheduled Jobs

Scheduled jobs let you run automated agent prompts on a recurring schedule. Each job is defined by a `job.md` file placed inside `.phosphor/jobs/<job-name>/`. Jobs run unattended — they operate in **yolo mode** (auto-approve all tool permissions), so they never get stuck waiting for human confirmation.

## How It Works

1. The cron service (`internal/platform/cron/service.go`) scans the jobs directory for `job.md` files.
2. Each job is parsed: YAML frontmatter for configuration, and the remaining body as the agent prompt.
3. When the cron schedule fires, the service creates or retrieves a session and sends the prompt to the agent via `AgentCoordinator.Run()`.
4. The agent has **full tool access** (same as the regular TUI) and runs in **yolo mode** so no permission prompts block execution.
5. Sessions are tagged with `service: "cron"` so they can be filtered and viewed separately in the TUI.

## Job File Structure

Jobs live in `.phosphor/jobs/<job-name>/job.md`. Each file contains:

```markdown
---
title: "Daily Summary"
description: "Summarize the day's work and commit history"
schedule: "0 9 * * *"   # Every day at 9:00 AM
session_mode: "persistent"
---

## Prompt

Generate a standup summary by checking recent git activity...
```

### Frontmatter Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | Yes | Human-readable job name |
| `description` | string | No | Purpose of the job |
| `schedule` | string | Yes | Standard cron expression (e.g., `"0 9 * * *"`) |
| `session_mode` | string | No | `persistent`, `ephemeral`, or `per_run` (default: `ephemeral`) |
| `delivery` | string array | No | Result delivery targets (future extension) |
| `session_id` | string | No | Explicit session ID for persistent mode |
| `allow_concurrent` | bool | No | Allow overlapping runs (default: `false`; uses `.job.lock` to prevent) |
| `failure_threshold` | int | No | Disable job after N consecutive failures (default: `0` = disabled) |

### Session Modes

- **`ephemeral` / `per_run`**: A new stateless session is created for each run and deleted afterward. Title includes a timestamp (`<job-name> <YYYY-MM-DD HH:MM:SS>`) so individual runs can be distinguished. Auto-summarization is **skipped** (stateless sessions are handled by the client).
- **`persistent`**: A stateful session is created once and reused across runs. Title is just the job name — no timestamp needed since it's the same session. Auto-summarization is **active** — the agent triggers summarization when token usage crosses the configured threshold (default 80% of context window).

#### Auto-Summarization Behavior for Persistent Sessions

Auto-summarization runs at the start of each agent turn. It works as follows:

1. The agent estimates `CurrentTokens` from the session's active messages.
2. Compares `CurrentTokens / ContextWindow` against `summarize_threshold` (default `0.8`).
3. If the fraction meets or exceeds the threshold, the session is summarized via the `Summarize` endpoint.

**Model/Context Window Changes Between Runs:**
- The threshold check uses the *current* model's `ContextWindow` at run time. If you switch models or update the context window config between job runs, the fraction recalculates automatically — no manual intervention needed. A larger context window means the same token count falls further below threshold; a smaller one triggers summarization sooner.

## Configuration

Enable the cron service in `phosphor.json`:

```json
{
  "services": {
    "cron": {
      "enabled": true,
      "jobs_directory": ".phosphor/jobs"
    }
  }
}
```

If `jobs_directory` is omitted, it defaults to `.phosphor/jobs`.

## Running the Cron Service

Start the scheduler with:

```bash
phosphor cron
```

This blocks until stopped (Ctrl+C). The service loads all jobs from the configured directory and registers them with the internal cron scheduler (`github.com/robfig/cron/v3`).

## Job Management CLI

```bash
phosphor job list          # List all scheduled jobs
phosphor job add <name>    # Interactive job creation
phosphor job remove <name> # Remove a job and its files
phosphor job edit <name>   # Open job.md in the default editor
```

## TUI Integration

Scheduled job sessions are managed through a dedicated section in the TUI command menu:

### Job Sessions
Accessible via the command menu, the **Job Sessions** view lists all sessions tagged with `service: "cron"`. Regular sessions are filtered out to avoid cluttering the main session list. Users can open, inspect, rename, or delete these sessions.

### Scheduled Jobs Dialog
The **Scheduled Jobs** dialog (`internal/ui/dialog/jobs.go`) lists all loaded jobs and provides:
- **Enter** — Run the selected job immediately
- **e / Ctrl+e** — Edit the job's `job.md` in the default editor
- **↑↓** — Navigate the job list

### Accessing from the TUI
1. Open the command menu.
2. Navigate to the **Scheduled Jobs** section.
3. Select **Jobs** to manage job definitions or **Job Sessions** to inspect run history.
4. Press **Enter** on a job to run it on-demand — a new session is created with the suffix ` (manual)` (e.g., `"example (manual)"`).

## Failure Tracking

When a job run fails, the failure count is tracked. If `failure_threshold` is set and the failure count reaches that value, the job is temporarily disabled. It can be re-enabled by editing the `job.md` file (which resets the counter) or by resetting via the CLI.

## Yolo Mode (Auto-Approve)

**Scheduled jobs always run in yolo mode.** This is critical because jobs execute unattended — there is no human to approve permission prompts. The cron service sets `SkipPermissionRequests = true` on the config store before each job run and restores the previous value afterward. This ensures the agent can freely use all tools (bash, write, edit, etc.) without being blocked by confirmation dialogs.

## Example Job

```markdown
---
title: "Example Job"
description: "Summarize the day's work and commit history"
schedule: "0 9 * * *"
session_mode: "persistent"
---

## Prompt

Generate a standup summary by checking recent git activity and write it to a timestamped file.

**Steps:**
1. Run `git log --oneline --since="yesterday"` to get recent commits.
2. Create a summary covering completed tasks, in-progress tasks, and blockers.
3. Write the summary to `.phosphor/standup-{YYYY-MM-DD}.md` using today's date.
   - If no commits found, write "No commits since yesterday."
   - Always create the file regardless of git output.
```