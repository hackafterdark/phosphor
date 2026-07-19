# Embedded Inference & Embedding Models

## Overview

Phosphor can run small AI models entirely on your machine using two embedded engines:

- **dlgo**: Local inference engine for lightweight generation tasks (e.g. compaction summaries)
- **goformer**: Local embedding engine for text-embedding generation (e.g. semantic search)

Both are completely optional. They are designed to offset usage of the larger cloud-hosted "large" and "small" models, reducing API costs and improving response latency for tasks that can run on fast local models.

## Configuration

`embedded_models` lives at the root of `phosphor.json`. The `summarize_model` setting lives under `options`:

```json
{
  "embedded_models": {
    "inference": {
      "enabled": true,
      "model_path": "models/Qwen3.5-0.8B.Q4_K_M.gguf",
      "model_repo": "unsloth/Qwen3.5-0.8B-GGUF",
      "gpu": false,
      "options": {
        "temperature": 0.3,
        "top_p": 0.9,
        "top_k": 40,
        "max_tokens": 2048,
        "seed": 42
      }
    },
    "embedding": {
      "enabled": true,
      "model_path": "models/bge-small-en-v1.5",
      "model_repo": "BAAI/bge-small-en-v1.5"
    }
  },
  "options": {
    "summarize_model": "embedded"
  }
}
```

## Inference Model (dlgo)

### Purpose

The inference model handles lightweight generation tasks locally. The most common use case is **compaction** — summarizing conversation history to stay within context window limits. Setting `"summarize_model": "embedded"` routes compaction through the local GGUF model instead of the expensive cloud model.

### Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable or disable the inference engine |
| `model_path` | string | — | Path to a GGUF model file (relative or absolute) |
| `model_repo` | string | — | HuggingFace repo ID for auto-download |
| `gpu` | bool | `false` | Use Vulkan GPU acceleration (requires `-tags vulkan` build) |
| `options` | object | — | Sampling parameters |

### GPU Acceleration

To enable GPU acceleration, build Phosphor with the Vulkan tag:

```bash
go build -tags vulkan -o phosphor .
```

Set `"gpu": true` in the config. The model will attempt GPU offload; if GPU is unavailable it falls back to CPU.

## Embedding Model (goformer)

### Purpose

The embedding model generates fixed-length vector representations of text. These vectors power features like semantic search and codebase indexing. The embedding model runs CPU-only.

### Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable or disable the embedding engine |
| `model_path` | string | — | Path to a Safetensors model directory |
| `model_repo` | string | — | HuggingFace repo ID for auto-download |

### Auto-Download

When `model_repo` is set, Phosphor downloads the model from HuggingFace on startup. Embedding models are stored as directories containing config files and weights.

## HuggingFace Authentication

Private or gated models require a `HF_TOKEN` environment variable. Generate a read token at https://huggingface.co/settings/tokens and export it:

```bash
export HF_TOKEN="hf_your_token_here"
```

The downloader automatically reads `HF_TOKEN` and passes it to HuggingFace.

## Troubleshooting

**401 Unauthorized**: Set `HF_TOKEN` with a valid HuggingFace read token.

**GPU not detected**: Ensure the binary was built with `-tags vulkan`. Without the build tag, `gpu: true` has no effect.

**Model file not found**: Verify `model_path` exists on disk, or set `model_repo` to trigger auto-download.

**Auto-download fails**: Check internet connectivity and that `HF_TOKEN` is set for gated repos.