# The `/goal` Slash Command & Session Goals

The goal feature turns Phosphor into a **self-driving agent** for a single
objective. You give it one goal, it keeps working across turns until the
objective is verifiably complete — without you having to nudge it.

The `/goal` slash command manages the active session goal (view, set, or
clear). The goal **runtime** is the engine that keeps the agent running toward
that goal.

---

## Why use a goal (vs. just prompting in yolo mode)

In plain yolo mode the agent runs autonomously *within a single turn*, but the
moment it decides it is done and produces a final no-tool-call response, the
turn ends and it waits for you. If it stops early — stops after partial work,
or hits an error/blocked path and stalls — you have to manually "kick" it
("are you stuck? can you continue?") to get it going again.

A goal adds four things on top of yolo:

1. **Anti-premature-stop.** When the agent ends a turn but the goal is still
   `active`, the runtime detects the stop and automatically injects a fresh
   *continuation turn* that says "keep working toward the objective." This is
   the main value: it survives the model quitting early or stalling on an
   error.
2. **A persistent objective anchor.** The objective is re-injected into the
   system prompt on *every* turn (including the very first one), so the task
   stays in focus as context grows.
3. **Completion discipline.** Before the agent is allowed to mark the goal
   done, it must audit every requirement against real evidence (files, tests,
   build output, runtime behavior) — not just "I made progress."
4. **Lifecycle controls yolo does not have:** pause / resume / clear, a live
   goal pill and sidebar panel, an elapsed-active-time timer, and a runaway
   guardrail (below).

A good mental model: yolo = "run this one turn to the end, on your own."; a
goal = "keep running turns until this objective is genuinely finished."

---

## How it works

```
user prompt ──► agent turn ──► (turn ends)
                                  │
                          goal status == active?
                          session idle & nothing queued?
                                  │ yes
                          budget remaining?
                          ├─ yes ──► inject continuation turn ──► agent turn …
                          └─ no  ──► AUTO-PAUSE the goal, notify user
```

- When a run finishes cleanly, the coordinator calls the goal runtime's
  `OnTurnFinished`, which decides whether to start another synthetic turn
  (`pkg/goal/runtime.go`).
- Each continuation is a real turn driven by a **continuation prompt** that
  re-states the objective, forbids shrinking it to fit one turn, and requires
  a completion audit before `update_goal(status="complete")`.
- The loop continues until either:
  - the model calls **`update_goal(status="complete")`** (only accepted when
    every requirement is verified), or
  - you **pause** / **clear** the goal, or
  - the **continuation budget** is exhausted (see below).

The objective is treated as **user-provided data**, not as higher-priority
instructions, so it can never be used to override the agent's rules.

---

## Slash commands

All goal commands require an active session.

### Set a goal
```
/goal <objective>
```
Sets a new goal with the provided text and immediately starts the autonomous
loop. You can then just sit back — the agent will keep working until done or
paused.

### View the current goal
```
/goal
```
Shows the objective text of the active goal (or `No active goal for this
session.` if none).

### Clear the goal
```
/goal clear
```
Removes the active goal and stops the loop.

> **Note:** `pause` and `resume` are **not** slash commands. They live in the
> Commands menu — see below.

---

## Pause / Resume / Set / Clear from the Commands menu

Open the **Commands** menu with **`Ctrl+P`** (the `/menu` command opens the
same dialog). Depending on the current goal state it shows:

- **Set Goal** — enter a new objective.
- **Pause Goal** — stop the autonomous loop while keeping the goal and its
  progress (status becomes `paused`).
- **Resume Goal** — start it running again from where it paused. Resuming
  grants a **fresh continuation budget**, so a goal that hit the limit gets
  another window once you have consciously reviewed it.
- **Clear Goal** — remove it.

The active goal is always visible as a pill in the status bar and as a
`GOAL (active|paused)` panel in the sidebar, with elapsed active time.

---

## The `update_goal` tool

While a goal is active, the agent is given the `update_goal` tool. Its only
valid argument is `status="complete"`. It:

- Fails if there is no active goal.
- Rejects a completion whose goal ID does not match the running one (stale
  protection).
- **Should only be called** once the agent has verified every requirement of
  the objective against current evidence.

When there is **no** active goal, the tool is hidden from the agent entirely,
so it is never offered as noise.

---

## Runaway guardrail: the continuation budget

Because the loop can keep going on its own, there is a hard stop so a goal the
model never marks complete (or that keeps stalling and retrying) cannot burn
tokens forever while you are away.

After the runtime starts `max_continuations` synthetic continuation turns for a
goal, it **auto-pauses** the goal and notifies you:

```
Goal paused in "<session>" — review progress, then open the Commands menu
(Ctrl+P) and choose "Resume Goal".
```

From there you inspect what it got to and either **Resume** (grants another
budget window) or **Clear**. This is also the recovery path for "it got stuck
on an error and kept going" — instead of looping on the failure forever, it
stops at the budget and hands control back to you.

### Configuring the budget

`options.agent.max_continuations` in `phosphor.json`:

```json
{
  "options": {
    "agent": {
      "max_continuations": 15
    }
  }
}
```

| Value     | Behavior                                                        |
|-----------|-----------------------------------------------------------------|
| `0`       | (default) use the built-in limit of **25** continuations.       |
| `N > 0`   | pause after **N** continuation turns.                            |
| negative  | **unlimited** — disable the guardrail (run until completion).    |

Resuming a goal resets its counter, so each resume grants one fresh window.

---

## Related

- `docs/commands/MENU.md` — the Commands dialog where Pause/Resume live.
- `docs/SYSTEM_PROMPT.md` — how the system prompt is assembled (the active
  goal block and the `<todo_list>` are injected dynamically per turn).
