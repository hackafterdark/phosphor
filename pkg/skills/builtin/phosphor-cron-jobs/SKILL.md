---
name: phosphor-cron-jobs
description: Use when the user wants to schedule a recurring agent task, set up a cron job, or create, edit, or debug a job.md file under .phosphor/jobs — covers the frontmatter schema, valid session_mode values, cron expressions, and how the cron service runs jobs unattended.
---

# Phosphor Cron Jobs

Scheduled jobs are `job.md` files under `.phosphor/jobs/<job-name>/`. The cron
service loads them at startup, parses YAML frontmatter for configuration, and
sends the markdown body to the agent on schedule. Jobs run unattended in
**yolo mode** (auto-approve all tool permissions).

For the full reference, see `docs/platform/SCHEDULED_JOBS.md`.

## Job File Structure

```markdown
---
title: "Daily Summary"
description: "Summarize the day's work and commit history"
schedule: "0 9 * * *"
session_mode: "ephemeral"
---

## Prompt

Generate a standup summary by checking recent git activity...
```

The file must start with `---`, and the body after the closing `---` is the
agent prompt verbatim.

## Frontmatter Fields

| Field               | Type        | Required | Description                                                        |
| ------------------- | ----------- | -------- | ------------------------------------------------------------------ |
| `title`             | string      | Yes      | Human-readable job name                                            |
| `description`       | string      | No       | Purpose of the job                                                 |
| `schedule`          | string      | Yes      | Standard cron expression (e.g., `"0 9 * * *"`)                     |
| `session_mode`      | string      | No       | `persistent`, `ephemeral`, or `per_run` (default: `ephemeral`)     |
| `delivery`          | string arr. | No       | Result delivery targets (future extension)                         |
| `session_id`        | string      | No       | Explicit session ID for `persistent` mode                          |
| `allow_concurrent`  | bool        | No       | Allow overlapping runs (default: `false`)                          |
| `failure_threshold` | int         | No       | Disable job after N consecutive failures (default: `0` = disabled) |

## Session Modes

These are the **only** valid values — anything else (e.g. `new`, `fresh`,
`once`) fails the job at load with `invalid session_mode` and the job never
runs:

- **`ephemeral`** (default): a new stateless session per run, titled
  `<job-name> <timestamp>`, kept for inspection.
- **`per_run`**: like `ephemeral`, but the session is deleted after the run.
- **`persistent`**: one stateful session reused across runs, auto-summarized
  when token usage crosses the configured threshold.

## Enabling the Service

The cron service is opt-in in `phosphor.json`:

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

## Writing the Prompt

The body is a normal agent prompt with full tool access, but remember:

- Nobody is watching. Be explicit about steps and edge cases; state what to
  do when there is nothing to do (e.g. "if no commits found, write 'No commits
  since yesterday'").
- The job cannot ask for permission or clarification — prompts that depend on
  a human answer will stall or fail.
- Prefer idempotent actions; overlapping runs are blocked by a lock file
  unless `allow_concurrent: true`.

## Debugging

- Invalid frontmatter or unknown `session_mode` values are rejected at load
  time with an `invalid session_mode` error in the logs; the job is skipped,
  not scheduled.
- Invalid cron expressions are rejected at schedule time with
  `invalid schedule`.
- Failing runs increment a counter; at `failure_threshold` the job disables
  itself until the `job.md` is edited.
- Inspect runs in the TUI under **Job Sessions** (sessions tagged
  `service: "cron"`).