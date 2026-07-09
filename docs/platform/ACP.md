# Agent Client Protocol (ACP)

## Overview

Phosphor implements the [Agent Client Protocol (ACP) v1](https://github.com/agentclientprotocol/agentclientprotocol) as a **stdio JSON-RPC server**. ACP is a protocol created by JetBrains and Zed Industries to standardize communication between code editors/IDEs and coding agents.

The IDE acts as the **client**, launching `phosphor acp` as a subprocess and exchanging newline-delimited JSON messages over its stdin/stdout. Phosphor is the **agent server**, handling the full ACP lifecycle from initialization through streaming prompt turns to session teardown.

---

## Launching ACP

```bash
phosphor acp
```

This starts the ACP service as a stdio JSON-RPC server. The IDE (Zed, JetBrains IDEs) spawns this process and communicates via stdin/stdout. Each message is a single line of JSON terminated by a newline character.

---

## Protocol Envelope

All messages follow the [JSON-RPC 2.0](https://www.jsonrpc.org/specification) format:

### Requests (with response)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/new",
  "params": { ... }
}
```

### Responses

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { "sessionId": "..." }
}
```

### Notifications (no response expected)

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": { ... }
}
```

### Errors

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32601,
    "message": "Method not found"
  }
}
```

---

## Protocol Methods

### `initialize`

The first message the IDE sends. Establishes the connection and negotiates capabilities.

**Request:**

```json
{
  "protocolVersion": 1,
  "clientInfo": {
    "name": "Zed",
    "title": "Zed",
    "version": "0.168.0"
  },
  "clientCapabilities": {
    "terminal": true,
    "sessionCapabilities": { "close": {}, "delete": {}, "resume": {} },
    "promptCapabilities": { "image": true, "audio": true }
  }
}
```

**Response:**

```json
{
  "protocolVersion": 1,
  "agentInfo": {
    "name": "phosphor",
    "title": "Phosphor",
    "version": "0.1.0"
  },
  "agentCapabilities": {
    "sessionCapabilities": {
      "loadSession": true,
      "close": {},
      "delete": {},
      "resume": {},
      "setMode": {}
    },
    "promptCapabilities": { "image": true, "audio": true, "embeddedContext": true }
  },
  "authMethods": []
}
```

The IDE uses `clientCapabilities` to declare what it supports (terminal access, file operations, media types). The agent responds with its own capabilities and available authentication methods.

---

### `session/new`

Creates a new workspace and conversation session. This is the primary entry point for starting an interaction.

**Request:**

```json
{
  "sessionId": "",
  "cwd": "/path/to/project",
  "mode": "default",
  "mcpServers": [
    {
      "name": "my-server",
      "stdio": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
      }
    }
  ],
  "additionalDirectories": []
}
```

**Response:**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

**What happens:**

1. A backend workspace is created from the `cwd` path, with a unique client ID to prevent hold expiry teardown.
2. A session is created within that workspace (title: `"ACP Session"`).
3. The service subscribes to workspace events via pubsub and starts a `fanOutEvents` goroutine to translate backend events into ACP notifications.
4. A stream client is attached to the workspace so it doesn't get torn down due to inactivity.

If `sessionId` is provided in the request, it is used as-is (stateful session). Otherwise a new UUID is generated.

---

### `session/load`

Resumes an existing session and replays its full message history as streaming notifications. This lets the IDE reconstruct the conversation UI from SQLite.

**Request:**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response:**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

**What happens:**

1. The backend workspace containing the session is located.
2. All messages are listed from SQLite and replayed as `user_message_chunk` / `agent_message_chunk` notifications — the IDE sees the same streaming events it would get for new turns.
3. Event subscription and fan-out begin, just like `session/new`.

---

### `session/prompt`

Sends a user message to the agent for processing. This is the core interaction method.

**Request:**

```json
{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "content": [
    { "type": "text", "text": "Refactor the login function to use JWT tokens" }
  ]
}
```

The `prompt` field is an alias used by Zed (instead of `content`). Both are accepted.

**Response (immediate, does not wait for completion):**

```json
{ "stopReason": "end_turn" }
```

The response returns immediately — actual agent output arrives asynchronously via `session/update` notifications. The `stopReason` indicates why the turn stopped; `end_turn` means the agent finished normally.

Other stop reasons: `max_tokens`, `max_turn_requests`, `refusal`, `cancelled`, `error`.

**Content blocks** support multiple types:

| Type | Fields | Description |
|------|--------|-------------|
| `text` | `text` | Plain text message |
| `image` | `data` (base64), `mimeType` | Image content |
| `audio` | `audio` (base64) | Audio content |
| `resource` | `uri`, `mimeType`, `text`/`blob`, `name` | Embedded resource |
| `resource_link` | `uri`, `mimeType`, `name` | Reference to accessible resource |

---

### `session/cancel`

Cancels an ongoing prompt turn.

**Request (notification, no response):**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

The agent sends a `[turn cancelled]` notification back to the IDE.

---

### `session/close`

Closes a session and frees resources. The session history remains in SQLite for future reloading.

**Request:**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response:** `{}`

**What happens:** Session context is cancelled, the session is removed from the in-memory map, and the stream client is detached from the workspace.

---

### `session/delete`

Permanently deletes a session and all its messages from history.

**Request:**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response:** `{}`

---

### `session/resume`

Reconnects to an existing session without replaying history. Useful when the IDE reconnects after a disconnect — the conversation continues from where it left off without re-streaming past messages.

**Request:**

```json
{ "sessionId": "550e8400-e29b-41d4-a716-446655440000" }
```

**Response:** `{ "sessionId": "..." }`

---

### `session/set_mode`

Switches the agent's operating mode for a session (e.g., from `"default"` to a task-oriented mode).

**Request:**

```json
{ "sessionId": "...", "mode": "default" }
```

**Response:** `{}`

---

### `session/request_permission`

The agent sends this **notification** when a tool call requires user authorization. The IDE responds with a JSON-RPC **request** containing the user's decision.

**Agent notification:**

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "...",
    "update": {
      "sessionUpdate": "tool_call",
      "toolCallId": "...",
      "title": "Edit file",
      "kind": "edit",
      "status": "pending"
    }
  }
}
```

Then the agent sends `session/request_permission`:

```json
{
  "id": 42,
  "method": "session/request_permission",
  "params": {
    "sessionId": "...",
    "toolCall": {
      "toolCallId": "...",
      "toolName": "edit",
      "title": "Edit file",
      "kind": "edit"
    },
    "options": [
      { "optionId": "1", "name": "Allow", "kind": "allow_once" },
      { "optionId": "2", "name": "Always Allow", "kind": "allow_always" },
      { "optionId": "3", "name": "Deny", "kind": "reject_once" },
      { "optionId": "4", "name": "Always Deny", "kind": "reject_always" }
    ]
  }
}
```

**IDE response (JSON-RPC request back to agent):**

```json
{
  "id": 42,
  "method": "session/request_permission",
  "params": {
    "outcome": {
      "outcome": "allow_once",
      "optionId": "1"
    }
  }
}
```

The agent maps the outcome to a `PermissionAction` and calls `backend.GrantPermission()`. If the IDE doesn't respond or an error occurs, the action defaults to deny.

---

### `logout`

Ends an authenticated session. Currently a no-op placeholder.

---

## Streaming Architecture

### Event Flow

```
Agent → pubsub broker → ACP fanOutEvents → JSON-RPC notifications → IDE stdout
```

The ACP service subscribes to workspace events via `backend.SubscribeEvents()`, which returns a channel of `pubsub.Event[tea.Msg]`. A dedicated goroutine (`fanOutEvents`) reads from this channel and translates each event into an ACP notification.

### Event Translation

Three payload types are handled:

| Payload Type | Handler | Notification |
|-------------|---------|--------------|
| `message.Message` | `handleMessage()` | `session/update` with text/tool chunks |
| `notify.Notification` | `handleNotification()` | `session/update` with finish/error events |
| `permission.PermissionRequest` | `handlePermissionRequest()` | `session/request_permission` |

### Text Streaming Deduplication

To avoid sending duplicate text to the IDE, the service tracks emitted lengths per message:

- **`seenText[msgID]`** — last emitted text length for assistant responses
- **`fullText[msgID]`** — accumulated full text for the final consolidated chunk
- **`seenThinking[msgID]`** — same pattern for reasoning/thinking content

Each streaming delta contains only the new characters since the last emission. On agent finish, a final consolidated chunk with `messageId` is emitted — this tells Zed to group all previous deltas into a single visible response bubble.

### Tool Call Tracking

Tool calls are tracked via `seenToolCallStatus[toolCallID]` to avoid duplicate status emissions. The IDE sees the full lifecycle:

```
pending → in_progress → completed / failed
```

---

## Session Update Notification

All live agent output flows through the `session/update` notification with a discriminated union `SessionUpdate`:

| Variant | Description |
|---------|-------------|
| `user_message_chunk` | User message being streamed (from history replay or prompt) |
| `agent_message_chunk` | Agent response text delta |
| `agent_thought_chunk` | Agent reasoning/thinking content delta |
| `tool_call` | New tool call reported |
| `tool_call_update` | Tool call status/content update |
| `plan` | Agent execution plan entries |
| `usage_update` | Token usage and cost information |
| `mode_change` | Agent-initiated mode switch |

### Tool Call Kinds

| Kind | Description |
|------|-------------|
| `read` | File read operations |
| `edit` | File edit operations |
| `delete` | File deletion |
| `move` | File move/rename |
| `search` | Code search |
| `execute` | Command execution |
| `think` | Reasoning/thinking |
| `fetch` | URL fetching |
| `other` | Uncategorized |

### Plan Entries

Each plan entry has content, priority (`high`, `medium`, `low`), and status (`pending`, `in_progress`, `completed`).

### Usage Updates

Reports token usage with `used` (tokens consumed), `size` (context window size), and optional `cost` (amount + currency).

---

## Internal State Management

The service maintains several in-memory maps guarded by mutexes:

| Map | Purpose | Guard |
|-----|---------|-------|
| `sessions` | Active session state (sessionID → workspaceID, clientID, event channel) | `mu` |
| `seenText` / `fullText` | Streaming deduplication for assistant text | `seenMu` |
| `seenThinking` | Streaming deduplication for thinking content | `seenMu` |
| `seenToolCallStatus` | Tool call status deduplication | `seenMu` |
| `finalEmitted` | Tracks which message IDs already got the final consolidated chunk | `finalMu` |
| `lastAssistantMsg` | Maps sessionID → last assistant message ID for finish detection | `mu` |
| `pending` | In-flight prompt responses keyed by JSON-RPC request ID | `pendingMu` |
| `clientRequests` | Client-initiated requests (e.g., permission responses) keyed by request ID | `clientRequestsMu` |

---

## Key Files

| File | Role |
|------|------|
| `internal/platform/acp/service.go` | Main service: JSON-RPC dispatch, session lifecycle, event fan-out, streaming deduplication |
| `internal/platform/acp/types.go` | Protocol type definitions (requests, responses, notifications, content blocks) |
| `internal/cmd/acp.go` | CLI entry point (`phosphor acp`) |
| `internal/backend/session.go` | Backend methods used by ACP (CreateSession, ListSessionMessages, SendMessage, etc.) |
| `internal/pubsub` | Event broker that connects the agent core to the ACP fan-out goroutine |
