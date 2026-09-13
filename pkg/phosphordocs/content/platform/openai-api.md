# OpenAI-Compatible API

Phosphor exposes an OpenAI-compatible HTTP API that lets any OpenAI SDK
client (Open WebUI, LangChain, custom scripts, etc.) talk to a Phosphor
agent workspace. It accepts requests in OpenAI's wire format and routes
them through Phosphor's backend to the agent core — including tools, hooks,
permissions, and streaming.

## Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/health` | GET | Liveness check. Returns `OK`. |
| `/v1/chat/completions` | POST | Chat completions (the primary endpoint). OpenAI `messages` format. |
| `/v1/responses` | POST | OpenAI Responses API format. Alternative to chat completions. |
| `/v1/models` | GET | Lists available models. |

## Configuration

Enable the service in `phosphor.json`:

```jsonc
{
  "services": {
    "openai-api": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 8643,
      "auth": {
        "type": "bearer",
        "key": "your-secret-token"
      },
      // See options below
      "accept_system_prompt": false,
      "log_request_body": false
    }
  }
}
```

### Service Entry Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | `bool` | — | Whether the service is running. |
| `host` | `string` | `"127.0.0.1"` | Address to bind to. |
| `port` | `int` | `8643` | Port number. |
| `auth` | `object` | — | Authentication config (see [Authentication](#authentication)). |
| `accept_system_prompt` | `bool` | `false` | Whether to accept a system prompt from API clients. **Disabled by default.** When enabled, Phosphor uses the client-supplied system prompt instead of its own template/profile system prompt for that turn only. See [System Prompt Override](#system-prompt-override). |
| `log_request_body` | `bool` | `false` | Log raw request bodies at Debug level. Useful for debugging client compatibility (e.g. Open WebUI request format). Contains user prompts — enable only when needed. See [Request Body Logging](#request-body-logging). |

### Authentication

Set `auth.type` to `"bearer"` and `auth.key` to a shared secret. Every
request must include:

```
Authorization: Bearer your-secret-token
```

If `auth` is omitted or `type` is `"none"`, no authentication is required
(dev mode).

### `accept_system_prompt`

When `false` (default), Phosphor ignores any system prompt sent by the API
client and uses its own system prompt engine (Go templates + profiles from
`internal/agent/templates/`).

When `true`, Phosphor accepts a system prompt from the client request and
uses it **for that single turn only**. The agent's stored system prompt is
not mutated — subsequent turns revert to the template/profile prompt.

**How clients send it:**

- **OpenAI chat completions format**: Include a `{role: "system"}` message
  as the first element of the `messages` array:

  ```json
  {
    "model": "gpt-4o",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user",   "content": "Hello"}
    ]
  }
  ```

- **Open WebUI format**: The system prompt is sent as `params.system` at
  the top level of the request body. Phosphor's OpenAI handler currently
  reads system prompts from the `messages` array only — `params.system`
  is not yet parsed. If you need OWL compatibility, open an issue.

**Why disabled by default:** Phosphor's system prompt engine enforces safety
rules, communication style, workflow conventions, and tool-use protocols.
Bypassing it with a client-supplied prompt could undermine these controls.
Enable only when you control the client and understand the tradeoff.

### `log_request_body`

When `true`, every chat completion request logs the raw JSON body at
**Debug** level:

```
DEBUG OpenAI request body  method=POST path=/v1/chat/completions body_size=2048 body={"chat":{...}, "messages":[...], ...}
```

- Body is truncated to 4 KB to prevent log bloat from large conversations.
- Auth tokens are not in the body (they're HTTP headers), so no redaction
  is needed.
- Requires Debug-level logging to actually see these entries. Set either:
  - ``debug`: true` at the config root, or
  - ``logging`: { `level`: `debug` }` in your config.

**Security consideration:** The body contains user prompts and any content
the client includes. Enable only for debugging and disable afterward.

## Session Handling

Phosphor uses a two-tier session model for the OpenAI API:

### Named Sessions (Stateful)

When the client sends an `X-Phosphor-Session-Id` header or a
`session_id` in the request body, Phosphor uses that specific session.
Full conversation history is loaded from SQLite into the agent's context.
These sessions are visible in the TUI sidebar.

```bash
curl -H "X-Phosphor-Session-Id: my-session-uuid" \
  http://127.0.0.1:8643/v1/chat/completions \
  -d '{"messages": [{"role": "user", "content": "Continue our discussion"}]}'
```

### Default Session (Stateless)

When no session ID is provided, Phosphor creates or reuses a single
stateless "default" session per workspace. This session:

- Persists messages to SQLite for audit trail
- Does **not** load history into the agent's context (the client already
  sent full conversation history in the request body)
- Is auto-named from the first user prompt
- Appears as a single entry in the TUI (not N "Untitled" sessions)

Every response includes the session ID in the
`X-Phosphor-Session-Id` header so clients can opt into stateful mode:

```bash
# First request — creates default session, returns its ID
$ curl -i http://127.0.0.1:8643/v1/chat/completions \
  -d '{"messages": [{"role": "user", "content": "Hello"}]}'
# Response header: X-Phosphor-Session-Id: abc-123-def

# Subsequent request — reuse the same session for full history
$ curl -H "X-Phosphor-Session-Id: abc-123-def" \
  http://127.0.0.1:8643/v1/chat/completions \
  -d '{"messages": [{"role": "user", "content": "Follow up"}]}'
```

## Request Format

### Chat Completions (`/v1/chat/completions`)

Accepts the standard OpenAI `chat/completions` request body:

```json
{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "..."},   // only used if accept_system_prompt=true
    {"role": "user",   "content": "Hello"},
    {"role": "assistant", "content": "Hi!"},
    {"role": "user",   "content": "How are you?"}
  ],
  "stream": false,
  "temperature": 0.7,
  "max_tokens": 4096,
  "top_p": 0.9,
  "seed": 42,
  "stop": ["\n\nHuman:", "\n\nAssistant:"],
  "session_id": "optional-session-id"
}
```

### Supported Fields

| Field | Type | Description |
|---|---|---|
| `model` | `string` | Model ID. Resolved against configured providers. |
| `messages` | `array` | Conversation messages in OpenAI format. The last `role: "user"` message is used as the prompt. Prior messages provide context. |
| `stream` | `bool` | If `true`, returns SSE stream. If `false`, waits for completion and returns the full response. |
| `temperature` | `number` | Sampling temperature. Overrides model config default. |
| `max_tokens` | `integer` | Maximum output tokens. Overrides model config default. |
| `top_p` | `number` | Nucleus sampling parameter. |
| `top_k` | `integer` | Top-k sampling parameter. |
| `frequency_penalty` | `number` | Frequency penalty. |
| `presence_penalty` | `number` | Presence penalty. |
| `seed` | `integer` | Random seed for reproducible outputs. |
| `min_p` | `number` | Min-p sampling parameter. |
| `repetition_penalty` | `number` | Repetition penalty. |
| `stop` | `array of string` | Stop sequences. |
| `top_logprobs` | `integer` | Number of top log probabilities per token. |
| `max_thinking_tokens` | `integer` | Maximum tokens for chain-of-thought reasoning. |
| `session_id` | `string` | Optional session ID. Falls back to `X-Phosphor-Session-Id` header, then creates/reuses default session. |
| `stream_options` | `object` | `{ "include_usage": true }` to include usage stats in stream chunks. |

### Sampling Parameters

All sampling parameters (`temperature`, `top_p`, `seed`, etc.) are passed
through from the API request to the agent coordinator. When provided, they
override the model's configured defaults. When omitted, the model config
defaults apply.

This applies to both chat completions and responses API endpoints.

### Responses API (`/v1/responses`)

Alternative endpoint accepting OpenAI's Responses format:

```json
{
  "model": "gpt-4o",
  "input": "Hello, help me with X",
  "stream": false
}
```

The `input` field is a string (the user prompt). Session handling uses the
same named/default model as chat completions.

## Response Format

### Non-Streaming

Standard OpenAI `chat.completion` response:

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "gpt-4o",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "Hello! How can I help you?"
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 25,
    "completion_tokens": 12,
    "total_tokens": 37
  }
}
```

Response headers include `X-Phosphor-Session-Id` for session persistence.

### Streaming

When `stream: true`, returns Server-Sent Events in OpenAI chunk format:

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

## Examples

### Basic Request (curl)

```bash
curl http://127.0.0.1:8643/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "What files are in the current directory?"}]
  }'
```

### With Authentication

```bash
curl http://127.0.0.1:8643/v1/chat/completions \
  -H "Authorization: Bearer my-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4o", "messages": [{"role": "user", "content": "Hello"}]}'
```

### Streaming Request

```bash
curl http://127.0.0.1:8643/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Write a poem"}],
    "stream": true
  }'
```

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8643/v1",
    api_key="my-secret-token",  # ignored if auth disabled
)

response = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

## Compatibility Notes

- Phosphor implements the OpenAI **chat completions** and **responses** API
  shapes but is not a drop-in replacement for OpenAI's infrastructure.
- Tool calls from the agent are processed by Phosphor's tool engine
  (bash, edit, view, grep, etc.) — the response content reflects tool
  output, not raw model tokens.
- The `messages` array supports the standard `role` values: `system`,
  `user`, `assistant`, `tool`.
- Image/content attachments in messages are filtered if the selected model
  doesn't support multimodal input.
