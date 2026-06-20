# Agent Context Chain

This document describes the different `context.Context` values used throughout
the Phosphor agent system, their purposes, and how they relate to each other.

Understanding this context chain is critical for correctly propagating values
like session IDs, span references, and other per-request metadata to tools and
callbacks.

## Table of Contents

- [Overview](#overview)
- [Context Hierarchy](#context-hierarchy)
- [Context Variables](#context-variables)
  - [`ctx` — Caller-supplied context](#ctx--caller-supplied-context)
  - [`agentCtx` — OTel-instrumented agent context](#agentctx--otel-instrumented-agent-context)
  - [`genCtx` — Cancellable generation context](#genctx--cancellable-generation-context)
  - [`callContext` — PrepareStep tool execution context](#callcontext--preparestep-tool-execution-context)
  - [`llmCtx` — LLM API call context](#llmctx--llm-api-call-context)
  - [`cleanupCtx` / `flushCtx` — Detached cleanup contexts](#cleanupctx--flushctx--detached-cleanup-contexts)
- [Context Value Keys](#context-value-keys)
- [Common Pitfalls](#common-pitfalls)
  - [Pitfall 1: Storing values in `ctx` instead of `agentCtx`](#pitfall-1-storing-values-in-ctx-instead-of-agentctx)
  - [Pitfall 2: Using `genCtx` where `ctx` is needed](#pitfall-2-using-genctx-where-ctx-is-needed)
- [Diagram](#diagram)

## Overview

The Phosphor agent uses a chain of contexts, each derived from the previous one,
to carry request-scoped data through different phases of an agent turn:

```
caller's ctx
  → agentCtx (OTel span attached, values stored here)
    → genCtx (cancellable, used for LLM streaming)
      → callContext (PrepareStep, used for tool execution)
        → llmCtx (LLM API call span attached)
```

Each context in the chain serves a distinct purpose and has specific cancellation
semantics.

## Context Hierarchy

```
coordinator.run(ctx)
  │
  │  ctx is passed to SessionAgent.Run()
  ▼
sessionAgent.Run(ctx, call)
  │
  │  otel.StartInvokeAgentSpan(ctx, ...) creates agentCtx
  │  agentCtx carries the OTel "invoke_agent" span
  ▼
agentCtx = context.WithValue(agentCtx, SessionID, ...)
  │
  │  context.WithCancel(agentCtx) creates genCtx
  │  genCtx is cancelled when the turn completes or user presses Escape
  ▼
genCtx = context.WithCancel(agentCtx)
  │
  │  Passed to fantasy.Agent.Stream() as the base for PrepareStep
  │  PrepareStep receives callContext (derived from genCtx)
  ▼
callContext (from PrepareStep callback)
  │
  │  May be wrapped with otel.StartLLMSpan() to create llmCtx
  │  Used for tool execution, message creation, etc.
  ▼
llmCtx = context.WithValue(callContext, LLM span, ...)
```

## Context Variables

### `ctx` — Caller-supplied context

**Origin:** The context parameter passed to [`coordinator.Run()`](internal/agent/coordinator.go:217), which
ultimately flows into [`sessionAgent.Run()`](internal/agent/agent.go:552).

**Purpose:** The root context for a single agent turn. Provided by the caller
(coordinator, server, CLI). Used for:

- Database operations (session fetch, message creation)
- OTel span creation (as the parent for the `invoke_agent` span)
- Timeout/cancellation from the caller side (e.g., HTTP request timeout)

**Key property:** This context is **NOT** cancelled when the turn completes.
It is only cancelled when the caller explicitly cancels it (e.g., user presses
Escape, HTTP request times out).

**Never create children of `ctx` for streaming operations** — use `agentCtx`
or `genCtx` instead, which have proper lifecycle management.

### `agentCtx` — OTel-instrumented agent context

**Origin:** Created at [`agent.go:559`](internal/agent/agent.go:559):

```go
agentCtx, agentTurnSpan := otel.StartInvokeAgentSpan(ctx, "Phosphor", call.SessionID)
agentCtx = context.WithValue(agentCtx, tools.AgentTurnSpanKey, agentTurnSpan)
```

**Purpose:** Carries the OTel `invoke_agent` span and all values stored in it.
This is the parent of `genCtx` and the base context for all tool execution.

**Key values stored here:**
- `tools.AgentTurnSpanKey` — the OTel span for the entire agent turn
- `tools.SessionIDContextKey` — **MUST be stored here** (not in `ctx`) so
  that all child contexts inherit it

**Critical:** All per-request values that tools need (session ID, model name,
etc.) must be stored in `agentCtx`, not in the original `ctx` parameter. This
is because `genCtx` is created as `context.WithCancel(agentCtx)`, not
`context.WithCancel(ctx)`.

### `genCtx` — Cancellable generation context

**Origin:** Created at [`agent.go:635`](internal/agent/agent.go:635) (accepted path)
or [`agent.go:779`](internal/agent/agent.go:779) (non-accepted path):

```go
genCtx, cancel = context.WithCancel(agentCtx)
```

**Purpose:** The cancellable context for the LLM streaming operation. Cancelled
when:
- The turn completes successfully
- The user presses Escape (cancellation)
- An error occurs

**Used for:**
- Passed to `fantasy.Agent.Stream()` for LLM streaming
- Message update callbacks (reasoning chunks, content chunks, tool calls)
- Session save operations after the turn

**Key property:** When `genCtx` is cancelled, all operations using it fail.
This is intentional — it allows cancelling an in-flight LLM call.

### `callContext` — PrepareStep tool execution context

**Origin:** The first parameter of the `PrepareStep` callback passed to
`fantasy.Agent.Stream()`. It is derived from `genCtx`.

**Purpose:** The context used during tool execution. This is the context that
tools see when they call `GetSessionFromContext(ctx)`.

**Key values set here:**
```go
callContext = context.WithValue(callContext, tools.MessageIDContextKey, assistantMsg.ID)
callContext = context.WithValue(callContext, tools.SupportsImagesContextKey, supportsImages)
callContext = context.WithValue(callContext, tools.ModelNameContextKey, modelName)
```

**Critical:** This is the context that flows into all tool implementations.
Any value that tools need must be present in `callContext` (which means it must
be stored in `agentCtx` before `genCtx` is created, or in `PrepareStep` before
tools are called).

### `llmCtx` — LLM API call context

**Origin:** Created inside `PrepareStep` at [`agent.go:875`](internal/agent/agent.go:875):

```go
llmCtx, llmSpan = otel.StartLLMSpan(callContext, provider, model)
callContext = llmCtx
```

**Purpose:** Carries the OTel `chat` span for a single LLM API call. This is a
child of the `invoke_agent` span.

**Used for:** The actual HTTP request to the LLM provider.

### `cleanupCtx` / `flushCtx` — Detached cleanup contexts

**Origin:** Created with `context.WithoutCancel()` to detach from the run
context:

```go
cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
flushCtx, flushCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
```

**Purpose:** Used for database writes and message flushes that must complete
even after the run context is cancelled (e.g., workspace shutdown).

**Key property:** These contexts are **detached** from the run context, so they
continue to work even when `ctx` is cancelled. They have a short timeout to
prevent hanging.

## Context Value Keys

All context values used by tools are defined in
[`internal/agent/tools/tools.go`](internal/agent/tools/tools.go):

| Key Type | Constant | Value Type | Used By |
|----------|----------|------------|---------|
| `sessionIDContextKey` | `SessionIDContextKey` | `string` | All tools (session ID lookup) |
| `messageIDContextKey` | `MessageIDContextKey` | `string` | Tools that need message context |
| `supportsImagesKey` | `SupportsImagesContextKey` | `bool` | View tool (image handling) |
| `modelNameKey` | `ModelNameContextKey` | `string` | Tools that need model info |
| `agentTurnSpanKey` | `AgentTurnSpanKey` | `trace.Span` | OTel span for the agent turn |
| `llmCallSpanKey` | `LLMCallSpanKey` | `trace.Span` | OTel span for the LLM call |

Access these via the helper functions in `tools/tools.go`:
- `GetSessionFromContext(ctx)`
- `GetMessageFromContext(ctx)`
- `GetSupportsImagesFromContext(ctx)`
- `GetModelNameFromContext(ctx)`

## Common Pitfalls

### Pitfall 1: Storing values in `ctx` instead of `agentCtx`

**Bug:** Storing session ID or other values in the original `ctx` parameter:

```go
// WRONG: ctx is NOT the parent of genCtx
ctx = context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)
genCtx, cancel = context.WithCancel(agentCtx)  // genCtx won't have session ID!
```

**Fix:** Store values in `agentCtx`:

```go
// CORRECT: agentCtx IS the parent of genCtx
agentCtx = context.WithValue(agentCtx, tools.SessionIDContextKey, call.SessionID)
genCtx, cancel = context.WithCancel(agentCtx)  // genCtx inherits session ID
```

This was the bug fixed in the session ID fix (June 2026).

### Pitfall 2: Using `genCtx` where `ctx` is needed

Some operations must succeed even if the turn is cancelled (e.g., creating the
user message, saving session state). For these, use the original `ctx` or a
detached context:

```go
// CORRECT: Use ctx for operations that must succeed even on cancel
_, err := a.messages.Create(ctx, call.SessionID, ...)

// CORRECT: Use genCtx for streaming operations
agent.Stream(genCtx, call)
```

## Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                     coordinator.run(ctx)                            │
│                     (caller's context)                              │
└─────────────────────────┬───────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│                   sessionAgent.Run(ctx, call)                       │
│                                                                     │
│  otel.StartInvokeAgentSpan(ctx, ...)                                │
│         │                                                           │
│         ▼                                                           │
│  agentCtx, agentTurnSpan                                            │
│  ├─ AgentTurnSpanKey → agentTurnSpan                               │
│  └─ SessionIDContextKey → call.SessionID  ← MUST be here!          │
└─────────────────────────┬───────────────────────────────────────────┘
                          │ context.WithCancel(agentCtx)
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         genCtx                                      │
│                         (cancellable)                               │
│                                                                     │
│  Used for:                                                          │
│  - fantasy.Agent.Stream(genCtx, ...)                               │
│  - Message update callbacks                                        │
│  - Session save after turn                                         │
└─────────────────────────┬───────────────────────────────────────────┘
                          │ Passed to Stream()
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     PrepareStep callback                            │
│                                                                     │
│  callContext (derived from genCtx)                                  │
│  ├─ MessageIDContextKey → assistantMsg.ID                          │
│  ├─ SupportsImagesContextKey → bool                                │
│  ├─ ModelNameContextKey → modelName                                │
│  └─ (inherits SessionIDContextKey from agentCtx)                   │
│                                                                     │
│  May wrap with:                                                     │
│  otel.StartLLMSpan(callContext, ...)                                │
│         │                                                           │
│         ▼                                                           │
│  llmCtx (LLM API call span)                                         │
└─────────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│                       Tool Execution                                │
│                                                                     │
│  tools.GetSessionFromContext(callContext) → "session-123"          │
│  tools.GetMessageFromContext(callContext) → "msg-456"              │
└─────────────────────────────────────────────────────────────────────┘
```
