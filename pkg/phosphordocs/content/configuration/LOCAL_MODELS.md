# Running a Local Model (OpenAI-Compatible Servers)

Any server that speaks the OpenAI chat-completions API works as an
`"type": "openai-compat"` provider. Define the provider with a `base_url`
pointing at your server, enable model discovery, and select one of its models:

```json
{
  "providers": {
    "local": {
      "name": "Local Server",
      "base_url": "http://localhost:11434/v1",
      "type": "openai-compat",
      "api_key": "$LOCAL_API_KEY",
      "discover_models": true
    }
  },
  "models": {
    "large": { "model": "qwen3-32b", "provider": "local" },
    "small": { "model": "qwen3-32b", "provider": "local" }
  }
}
```

## Typical Endpoints

| Server | Usual `base_url` | Notes |
|--------|------------------|-------|
| [Ollama](https://ollama.com) | `http://localhost:11434/v1` | `discover_models` lists installed models |
| [llama.cpp](https://github.com/ggml-org/llama.cpp) (`llama-server`) | `http://localhost:8080/v1` | Set `--port` to match |
| [vLLM](https://docs.vllm.ai) | `http://localhost:8000/v1` | See tool-call note below |
| [LM Studio](https://lmstudio.ai) | `http://localhost:1234/v1` | Start the local server from the app |

## Notes

- **API key.** Local servers usually don't check one. You can omit `api_key`,
  set a placeholder like `"not-needed"`, or reference an env var with `$VAR`
  (values are shell-expanded at config load, `$(some-command)` works too for
  key-retrieval commands).

- **Model lists.** With `"discover_models": true` Phosphor pulls the model
  list from `/v1/models`. List models explicitly instead (or in addition)
  when you want to attach metadata — the fields Phosphor uses from each entry
  are `context_window`, `default_max_tokens`, `can_reason`, and
  `supports_attachments` (plus sampling knobs under `options`; see the
  [model entry field table](PROVIDERS_AND_MODELS.md#model-entry-fields-models)
  for the full list):

  ```json
  {
    "providers": {
      "local": {
        "base_url": "http://localhost:8000/v1",
        "type": "openai-compat",
        "discover_models": false,
        "models": [
          {
            "id": "qwen3-32b",
            "name": "Qwen3 32B",
            "context_window": 32768,
            "default_max_tokens": 8192,
            "can_reason": true,
            "supports_attachments": false
          }
        ]
      }
    }
  }
  ```

- **Tool calling.** Local stacks are the one place where tool calls sometimes
  can't use the native function-calling path. If your server's parser expects
  `<tool_call>…</tool_call>` text instead of native tool calls (common with some
  vLLM/llama.cpp setups and chat templates), set `"tool_call_format": "xml"`
  on the provider.

- **Thinking toggles.** For models whose chat template has an
  "enable_thinking" kwarg, set `"enable_thinking": "on"` or
  `"enable_thinking": "off"` on the `models.large` / `models.small`
  selection to pin it; omitting it leaves the server default.

- **Server-specific fields.** Anything non-standard can ride along verbatim
  in request bodies via `extra_body` (openai-compat providers only):

  ```json
  {
    "providers": {
      "local": {
        "base_url": "http://localhost:8080/v1",
        "type": "openai-compat",
        "extra_body": { "repeat_penalty": 1.1 }
      }
    }
  }
  ```

- **Disable the cloud defaults** if you only ever run local, so nothing can
  accidentally phone home: `"options": { "disable_default_providers": true }`
  (with this on, every provider must be fully specified in your config).

## Verifying

Use `phosphor models` to verify what Phosphor can see per provider. For the
full field reference — every provider field, every sampling parameter,
per-provider support matrices — see
[PROVIDERS_AND_MODELS.md](PROVIDERS_AND_MODELS.md).

## Guided Setup

Pointing Phosphor at a local server is also available as a guided flow: open
the command palette (`Ctrl+P`) and pick **Add Custom Provider** — the wizard
collects the name, base URL, optional API key, and model ids, then writes the
provider to your global data config
(`~/.local/share/phosphor/phosphor.json`) for you. Pick **Manage Custom
Providers** to list your providers, edit any field (down to per-model sampling
options), or delete one.

## Where to Put This Config

You can place the provider block in any config file (see
[FILES_AND_MERGING.md](FILES_AND_MERGING.md) for the full merge order):

| File | Path |
|------|------|
| Global config | `~/.config/phosphor/phosphor.json` |
| Global data config | `~/.local/share/phosphor/phosphor.json` |
| Project config | `<project>/phosphor.json` |
| Workspace config | `<project>/.phosphor/phosphor.json` |

The TUI wizard writes to the **global data config**. If you prefer to hand-edit,
the global config (`~/.config/phosphor/phosphor.json`) is the natural place —
Phosphor never overwrites it.
