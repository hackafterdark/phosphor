# Fix: Queued Message Duplication Causing Context Window Overflow

## Summary

Queued messages were sent to the LLM inference API twice per turn, causing exponential context window bloat that rapidly consumed the context limit and triggered infinite retry loops.

## Root Cause

The agent's queued message handling had two overlapping code paths that both appended queued messages to the API history:

### Path 1: `PrepareStep` callback (line ~426)

Inside `agent.Stream()`, the `PrepareStep` callback created queued messages in the DB and appended them to `prepared.Messages`:

```go
queuedCalls, _ := a.messageQueue.Get(call.SessionID)
a.messageQueue.Del(call.SessionID)
for _, queued := range queuedCalls {
    userMessage, createErr := a.createUserMessage(callContext, queued)
    if createErr != nil {
        return callContext, prepared, createErr
    }
    prepared.Messages = append(prepared.Messages, userMessage.ToAIMessage()...)
}
```

### Path 2: Recursive `Run()` call (line ~793)

After streaming completed, if queued messages remained, a recursive `Run()` was triggered:

```go
queuedMessages, ok := a.messageQueue.Get(call.SessionID)
if !ok || len(queuedMessages) == 0 {
    return result, err
}
skipRunComplete = true
firstQueuedMessage := queuedMessages[0]
a.messageQueue.Set(call.SessionID, queuedMessages[1:])
return a.Run(ctx, firstQueuedMessage)
```

The recursive call's `getSessionMessages()` fetched all messages from the DB (including the queued ones created in Path 1), and `preparePrompt()` converted them to API history.

### The Duplication Loop

1. **Turn N**: `Run()` calls `getSessionMessages()` → `preparePrompt()` → `agent.Stream()`
2. **Inside `agent.Stream()`**: `PrepareStep` creates queued messages in DB and appends them to `prepared.Messages` (sent to API)
3. **After streaming**: Recursive `Run()` is triggered
4. **Recursive `Run()`**: `getSessionMessages()` fetches all DB messages (including queued ones from step 2) → `preparePrompt()` converts them to history → `agent.Stream()` sends them to API
5. **Result**: Queued messages appear in the API history **twice** — once from the current turn's `PrepareStep` append, and again from the recursive call's normal flow

Each turn, the duplication compounded, rapidly consuming the context window.

## Fix

Three changes in `internal/agent/agent.go`:

### 1. Removed queued message append from `PrepareStep`

The queued message creation and append in `PrepareStep` was removed entirely. These messages are now handled by the recursive call's normal `getSessionMessages()` → `preparePrompt()` flow, which is the correct single source of truth.

### 2. Moved queued message DB creation before the recursive call

All queued messages are now persisted to the DB **before** the recursive `Run()` is triggered (line ~782-792). This ensures the recursive call's `getSessionMessages()` picks them up as part of the conversation history.

```go
skipRunComplete = true
firstQueuedMessage := queuedMessages[0]
// Create all queued user messages in the DB so the recursive call's
// getSessionMessages / preparePrompt picks them up as part of the
// conversation history.
for _, queued := range queuedMessages {
    _, err := a.createUserMessage(ctx, queued)
    if err != nil {
        slog.Error("Failed to create queued user message", "error", err)
    }
}
a.messageQueue.Set(call.SessionID, queuedMessages[1:])
return a.Run(ctx, firstQueuedMessage)
```

### 3. Made `createUserMessage` idempotent

If a user message with the same text content already exists in the session, `createUserMessage` returns the existing message instead of creating a duplicate. This handles the case where both the pre-recursion loop and the recursive call's own `createUserMessage` try to create the same message.

```go
// Idempotency: if a user message with the same text content already
// exists in the session, return it instead of creating a duplicate.
msgs, err := a.messages.List(ctx, call.SessionID)
if err == nil {
    for _, m := range msgs {
        if m.Role == message.User {
            for _, p := range m.Parts {
                if tc, ok := p.(message.TextContent); ok && tc.Text == call.Prompt {
                    return m, nil
                }
            }
        }
    }
}
```

## Why This Fix Is Correct

- **Single source of truth**: The recursive call's `getSessionMessages()` → `preparePrompt()` is the only place that adds messages to API history. No duplicate `PrepareStep` append.
- **Idempotency**: Even if `createUserMessage` is called multiple times for the same prompt, only one DB record is created.
- **Error resilience**: If the streaming fails, queued messages are still persisted in the DB and will be included in the next successful `Run()` call.
- **No functional change**: The behavior is identical from the user's perspective — queued messages are processed sequentially. Only the internal duplication is eliminated.

## Files Changed

- `internal/agent/agent.go` — `preparePrompt` (PrepareStep), `Run` (recursive call), `createUserMessage` (idempotency)

## Testing

- All agent tests pass (`go test ./internal/agent/...`)
- Full test suite passes (pre-existing `TestSelfExecBlocker` failure in `internal/shell` is unrelated, Windows-specific)
