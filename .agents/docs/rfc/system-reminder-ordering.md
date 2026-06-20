# RFC: Move System Reminder to System Prompt Parameter

## Status

**Draft**

## Problem

The `<system_reminder>` message is injected as a **user message** at position 0 of every turn's conversation history:

```go
// internal/agent/agent.go:1017-1024
history = append(history, fantasy.NewUserMessage(
    fmt.Sprintf("<system_reminder>%s</system_reminder>", `...`),
))
```

This causes two problems:

1. **System instructions masquerading as conversation** — The model is trained to treat `role: "user"` messages as user input, not system instructions. Injecting system-level reminders as user messages conflates instructions with conversation history, which can confuse the model about which user message is the latest one to respond to.

2. **No separation of concerns** — System-level instructions (todo list reminders, feature hints) should be in the system message, not the conversation history.

## Current message flow

```
[0] User: <system_reminder>todo list is empty</system_reminder>   ← changes every turn
[1] User: "how do I define auto-summarization?"
[2] Assistant: (answers)
[3] User: "review unstaged changes"
[4] Assistant: (should answer [3])
```

The model should respond to [3] because it's the last user message. But the system_reminder at [0] is a user message that changes content every turn, which can interfere with the model's understanding of the conversation structure.

## Proposed solution

Move the system_reminder content into the actual system message via the `fantasy.PrepareStepResult.System` field. The fantasy library already supports dynamic system prompts per-call through the `PrepareStep` function.

### How it works

The fantasy library's `AgentOption` `WithPrepareStep()` allows injecting dynamic content per-call:

```go
PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
    prepared.Messages = options.Messages
    // ... existing code ...
    
    // NEW: Set dynamic system prompt content
    dynamicSystem := buildDynamicSystemPrompt()  // todo list state, etc.
    prepared.System = &dynamicSystem
    
    return callContext, prepared, nil
}
```

The `prepared.System` field is a `*string` that gets merged with the static system prompt set via `fantasy.WithSystemPrompt()` at agent creation time.

### Changes required

1. **`internal/agent/agent.go`** — Remove the user-message-based system_reminder injection from `preparePrompt()` (lines 1016-1025)

2. **`internal/agent/agent.go`** — In the existing `PrepareStep` function (line 417), set `prepared.System` to include the dynamic state (todo list reminders, etc.)

3. **`internal/agent/agent.go`** — Create a `buildDynamicSystemPrompt()` function that returns the current dynamic state as a string

### Implementation considerations

**CRITICAL: System prompt concatenation** — `fantasy.WithSystemPrompt()` sets the static system prompt at agent creation time. The `PrepareStep` function's `prepared.System` field may **overwrite** rather than append. If so, retrieve the static prompt and concatenate:

```go
staticPrompt := a.systemPrompt.Get()
dynamicPrompt := a.buildDynamicSystemPrompt()
combined := staticPrompt + "\n\n" + dynamicPrompt
prepared.System = &combined
```

Verify the actual behavior by checking how fantasy merges `WithSystemPrompt()` with `PrepareStepResult.System`. If fantasy already concatenates, the simpler form works. **Test this first** — an incorrect assumption here breaks the entire system prompt.

**CRITICAL: Context window tax** — The dynamic system prompt is sent on **every `Run()` call**, consuming tokens each time. Keep `buildDynamicSystemPrompt()` concise:

- Use short labels and abbreviations where possible
- Only include relevant state (e.g., active todo items, not completed ones)
- Consider summarizing long todo lists rather than listing all items
- Monitor token usage in early testing to catch unexpected bloat

This is fine for now, but keep an eye on it if you ever scale to very long conversation histories. If the todo list grows to 50 items, that's 50 items worth of tokens being sent to the model on every single turn.

**CRITICAL: State sanitization and truncation** — `buildDynamicSystemPrompt()` is called on every `Run()` and feeds directly into the API call. It **must not crash** the `PrepareStep` pipeline. Handle corruption gracefully:

- **Truncate oversized content** — If the todo list or any state exceeds a reasonable size (e.g., 2000 characters), truncate and append a note like `"[truncated, N items omitted]"`
- **Sanitize malformed strings** — Strip or escape control characters, null bytes, or other non-printable characters that could corrupt the prompt
- **Log errors, don't panic** — If state is corrupted, log a warning and return a safe fallback string (e.g., `"Dynamic state unavailable."`) rather than propagating an error that could abort the entire turn
- **Be idempotent** — Calling the function multiple times with the same state must produce the same output. No side effects, no shared mutable state

Example:

```go
func (a *sessionAgent) buildDynamicSystemPrompt() string {
    // Try to build the prompt, but never crash.
    defer func() {
        if r := recover(); r != nil {
            slog.Error("buildDynamicSystemPrompt panicked, using fallback", "recover", r)
        }
    }()
    
    items, err := a.getTodoItems()
    if err != nil {
        slog.Warn("Failed to read todo list, using fallback", "error", err)
        return "Dynamic state unavailable."
    }
    
    var sb strings.Builder
    for i, item := range items {
        // Sanitize: strip non-printable chars
        safe := sanitize(item.Text)
        sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, safe))
    }
    
    result := sb.String()
    // Truncate if too large
    maxLen := 2000
    if len(result) > maxLen {
        result = result[:maxLen] + "\n[truncated]"
    }
    
    return result
}

func sanitize(s string) string {
    // Strip non-printable characters except newlines and tabs
    var out []rune
    for _, r := range s {
        if unicode.IsPrint(r) || r == '\n' || r == '\t' {
            out = append(out, r)
        }
    }
    return string(out)
}
```

This ensures the `PrepareStep` pipeline never fails due to corrupted state, and the model always receives a valid, bounded system prompt.

## Why this approach

1. **Uses existing fantasy API** — No need to fork or shim the library. The `PrepareStep` function already exists and supports dynamic system prompts.

2. **Minimal code changes** — Remove 10 lines of user-message injection, add ~20 lines of system prompt building.

3. **Extensible** — Future dynamic state (actual todo list items, goal state, etc.) can be added to `buildDynamicSystemPrompt()` without changing the conversation history.

4. **Proper separation** — System instructions go in the system message, conversation goes in user/assistant messages.

## What we're NOT doing

- **No turn markers** — The model doesn't need explicit ordering signals. The API format provides this.
- **No sequence numbers** — Adding ordering to messages is unnecessary technical debt.
- **No DB migration** — The messages table doesn't need a sequence column for this fix.

## Testing

- **Unit tests**: Verify that `buildDynamicSystemPrompt()` returns the expected content
- **Integration tests**: Verify that the model responds to the latest user message in multi-turn conversations
- **Regression tests**: Ensure existing behavior (todo list, goal continuation) still works

## Related work

- **Loop detection fixes** (`internal/agent/loop_detection.go`): These address repeated reasoning/tool calls within a turn, not cross-turn ordering. They don't fix this problem.
- **Queued message deduplication** (`internal/agent/agent.go:969-984`): Prevents duplicate messages but doesn't address ordering.
- **Goal feature** (`internal/goal/runtime.go`): Uses a separate agent run with a continuation prompt, which works correctly because it's a distinct turn, not a message in the history.

## Future enhancements

Once the system_reminder is properly in the system prompt, we can extend `buildDynamicSystemPrompt()` to include:

- Actual todo list items (not just a reminder)
- Current goal state
- Other dynamic context that should be in the system message

This keeps the conversation history clean and focused on actual user-assistant dialogue.
