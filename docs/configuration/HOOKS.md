# Hooks

Shell scripts that fire on lifecycle events and can block, rewrite, or
annotate tool calls:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "name": "block-env-edits",
        "matcher": "^edit$",
        "command": "./.phosphor/hooks/gate.sh",
        "timeout": 30
      }
    ]
  }
}
```

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Identifier for the hook |
| `matcher` | string | Regex matched against the tool name |
| `command` | string | Shell command to execute |
| `timeout` | int | Timeout in seconds |

## Events

| Event | When it fires |
|-------|---------------|
| `PreToolUse` | Before a tool executes |
| `PostToolUse` | After a tool executes |
| `PreLLMCall` | Before sending to the LLM |
| `PostLLMCall` | After receiving from the LLM |

## Protocol

The command receives a JSON payload on stdin and returns a decision on
stdout. Full protocol, events, and examples:
[hooks/README.md](../hooks/README.md).
