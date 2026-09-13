# The `/stats` Slash Command

The `/stats` slash command opens the usage statistics dialog, which displays token consumption and model usage data.

---

## Usage

```
/stats
```

---

## Behavior

- Opens the usage stats dialog, which shows total tokens used, request counts, and per-model breakdowns.
- If the dialog is already open, it brings it to the front.
- Data is loaded asynchronously via `LoadUsageDataMsg`.