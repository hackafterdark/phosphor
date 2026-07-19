# The `/name` Slash Command

The `/name` slash command lets you view or rename the current session.

---

## Usage

### View the session name
```
/name
```
Displays the current session's title. Requires an active session.

### Rename the session
```
/name My New Session Title
```
Renames the current session to the provided title. Requires an active session. Whitespace is trimmed; empty names produce a warning.

---

## Behavior

- Without arguments, shows the current session name.
- With arguments, joins them as the new title and saves it.
- Reports a warning if no session is active.