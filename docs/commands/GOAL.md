# The `/goal` Slash Command

The `/goal` slash command lets you manage the active session goal — view, set, or clear it — directly from the prompt.

---

## Usage

### View the current goal
```
/goal
```
Shows the objective text of the active goal for the current session. Requires an active session.

### Clear the goal
```
/goal clear
```
Removes the active goal. Requires an active session.

### Set a new goal
```
/goal <objective>
```
Sets a new goal with the provided text. Requires an active session.

---

## Behavior

- If no session is active, the command reports a warning and does nothing.
- When called with no arguments, it displays the current goal status or an info message if none exists.
- When called with `clear`, it removes the goal.
- When called with free-form text, it sets that text as the goal objective.