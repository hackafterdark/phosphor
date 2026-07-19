# Pining and Unpinning Sessions

Pinning a session marks it as protected from bulk deletion operations. Pinned sessions are excluded from `BulkDeleteSessions` and `PruneMessages`.

---

## Usage

```
/pin
```
Toggles the pinned state of the currently active session. Running it again unpins the session.

---

## Behavior

- Pinned sessions appear at the **top** of the sessions list.
- Pinned sessions display a **★** icon in both the sessions list and the right sidebar.
- Pinned sessions survive automatic cleanup of old sessions.
- Only root sessions (`parent_session_id IS NULL`) can be pinned.