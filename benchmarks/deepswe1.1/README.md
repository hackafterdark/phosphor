# DeepSWE v1.1

Next-generation SWE benchmark kit for Phosphor, kept alongside
`benchmarks/swebench` (which stays the home for older-generation models that
haven't hit the SWE-bench Verified ceiling).

- **Benchmark:** Datacurve DeepSWE v1.1 — 113 long-horizon, contamination-free
  tasks across 91 repos in TS/Go/Python/JS/Rust, each graded by a hand-written
  program-based verifier in a separate container (v1.1 revision, 2026-09-22).
- **Harness:** [Pier](https://github.com/datacurve-ai/pier) (`datacurve-pier`,
  a Harbor fork). v1.1 grading requires Pier > 0.3.0.
- **Tasks:** https://github.com/datacurve-ai/deep-swe (Apache-2.0), cloned to
  `.tmp/deep-swe`. Environments are prebuilt ECR images
  (`public.ecr.aws/d3x8x8q7/swe-bench-202605:<ext_id>-v1.1`), 2 CPU / 8 GB,
  no-network, agent timeout 3 h.

## Files

| File | Purpose |
|---|---|
| `setup.sh` | Installs Pier into `.venv`, clones tasks, builds the container binary (`--build-binary`) |
| `run.sh` | Launches Pier with the Phosphor agent; `--task <id>` for single tasks |
| `score.sh` | Aggregates `verifier/reward.json` into a pass rate |
| `agents/phosphor.py` | Harbor `BaseAgent` adapter: uploads binary, injects arm config, runs `phosphor run`, commits |
| `arms/*.json` | Provider arms. Real arms carry literal keys and are gitignored; commit only `*.example.json` templates, which reference credentials via `${VAR:?...}` shell expansion |

## Quick start

```bash
benchmarks/deepswe1.1/setup.sh --build-binary   # pier venv + tasks + binary
OLLAMA_API_KEY=... benchmarks/deepswe1.1/run.sh --task abs-stepped-slices
benchmarks/deepswe1.1/score.sh
```

The default arm (`arms/deepseek-v41-flash.json`, gitignored because it holds
a literal key) reads its credential from the host environment via `--ae`. For
a committable arm, use the template's shell-expansion form —
`"api_key": "${OLLAMA_API_KEY:?OLLAMA_API_KEY is required}"` — phosphor
expands it at config load.

Full 113-task run: drop `--task`. `DEEPSWE_*` env vars in `run.sh` control
the binary, arm config, concurrency, and credential source.

### Network patching (required for hosted models)

DeepSWE declares `[agent] network_mode = "no-network"`, which blocks Ollama
Cloud / OpenRouter calls — the agent fails instantly with `network is
unreachable`. `prepare_tasks.sh` (run automatically by `setup.sh`) copies the
tasks to `tasks-open/` with `network_mode = "public"`; `run.sh` uses that
copy by default. The verifier environments are separate containers and grade
offline regardless. Leaderboard submissions should instead run on Modal where
the sandbox itself has egress.

## Scoring

Aggregate pass rate = resolved / submitted. Per leaderboard rules:
timeouts and context-window failures count as agent failures; provider/infra
errors must be excluded (`score.sh` lists trials without verifier output
separately — a score is only citable when that count is 0).

## Notes

- The v1.1 collect hook grades `git diff <base> HEAD`, so the agent **must
  commit**; `agents/phosphor.py` commits after `phosphor run` finishes.
- The binary must be the bullseye-built container binary
  (`.tmp/phosphor-cgo-231`); host builds fail on glibc 2.35 images.
- `run.sh` forces `DOCKER_CONFIG` into `.tmp/dockerconfig` — the verifier
  build dies with `mkdir ~/.docker: permission denied` on hosts where
  `$HOME` is restricted.
- The agent installs language servers into the container before the run
  (gopls / typescript-language-server / pyright / rust-analyzer), gated on
  the repo markers `go.mod`, `package.json`/`*.ts`, `pyproject.toml`/`setup.py`,
  and `Cargo.toml`. Phosphor's auto-LSP then starts whichever binary it finds
  (root markers match). Installs are best-effort: a failure degrades that
  trial to no-LSP. The per-trial `lsp_setup` metadata records the outcome.
  Egress for the installs rides on the `public` network mode above.
- Leaderboard parity requires mini-swe-agent; Phosphor runs are internal
  capability measurements, not leaderboard submissions.
- Epoch AI's review flagged grading defects in v1.0 tasks; v1.1's separate
  verifier environment addresses most, but treat single-task outliers with
  suspicion and check `verifier/test-stdout.txt` before believing a failure.
- Disk: concurrency is capped at 2 by default — each trial environment is a
  multi-GB image and sweeps leave images behind. Between runs (or when a run
  dies), reclaim with `docker image prune -f`; check pressure with
  `docker system df`.
