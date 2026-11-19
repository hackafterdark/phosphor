# DeepSWE v1.1

Next-generation SWE benchmark kit for Phosphor, kept alongside
`benchmarks/swebench` (which stays the home for older-generation models that
haven't hit the SWE-bench Verified ceiling).

## Results

Phosphor scores **78.8% on DeepSWE v1.1** (89/113 resolved, zero
infrastructure failures) with **DeepSeek V4.1 Flash** (`reasoning_effort=max`)
using this kit and the [Pier](https://github.com/datacurve-ai/pier) harness —
above the **74.2%** mini-SWE-agent baseline DeepSeek itself reports for the
model on its
[model card](https://huggingface.co/deepseek-ai/DeepSeek-V4.1-Flash), and
above every scaffold in the card's 8-harness sweep (65.5–74.2 on this
benchmark). The agent installs per-language servers (gopls,
typescript-language-server, pyright, rust-analyzer) into the task container
and runs with microcompaction enabled; reward-seeking egress (GitHub
lookups) is blocked through the host-side mitmproxy filter documented below.

The arm pins `options.agent.max_consecutive_tool_failures_headless` to 8 so the
retry budget is fixed in version control rather than inherited from a default
that can drift. Note the recorded run predates that default: its binary applied
the interactive cap of 2, and only 6 of 113 trials hit the guard (5 of those 6
resolved anyway), so the gap is worth at most ~1 task.

- **Benchmark:** Datacurve DeepSWE v1.1 — 113 long-horizon, contamination-free
  tasks across 91 repos in TS/Go/Python/JS/Rust, each graded by a hand-written
  program-based verifier in a separate container (v1.1 revision, 2026-09-22).
- **Harness:** [Pier](https://github.com/datacurve-ai/pier) (`datacurve-pier`,
  a Harbor fork). v1.1 grading requires Pier > 0.3.0.
- **Tasks:** https://github.com/datacurve-ai/deep-swe (Apache-2.0), cloned to
  `.tmp/deep-swe`. Environments are prebuilt ECR images
  (`public.ecr.aws/d3j8x8q7/swe-bench-202605:<ext_id>-v1.1`), 2 CPU / 8 GB,
  no-network, agent timeout 3 h.

## Files

| File | Purpose |
|---|---|
| `setup.sh` | Installs Pier into `.tmp/pier`, clones tasks, builds the container binary (`--build-binary`) |
| `run.sh` | Launches Pier with the Phosphor agent; `--task <id>` for single tasks |
| `score.sh` | Aggregates `verifier/reward.json` into a pass rate |
| `agents/phosphor.py` | Harbor `BaseAgent` adapter: uploads binary, injects arm config, runs `phosphor run`, commits |
| `arms/*.json` | Provider arms. Real arms carry literal keys and are gitignored; commit only `*.example.json` templates, which reference credentials via `${VAR:?...}` shell expansion |
| `tools/mitm_blocklist.py` | Optional mitmproxy addon: egress filter that blocks reward-seeking lookups (GitHub et al.) while keeping registries and the model API open |

## Quick start

```bash
benchmarks/deepswe1.1/setup.sh --build-binary   # pier venv + tasks + binary
OLLAMA_API_KEY=... benchmarks/deepswe1.1/run.sh --task abs-stepped-slices
benchmarks/deepswe1.1/score.sh
```

The default arm (`arms/deepseek-v41-flash.json`, gitignored because it holds
a literal key) reads its credential from the host environment via `--ae`. For
a committable arm, copy `arms/deepseek-v41-flash.example.json` to a new
arm file and fill in the real values (or leave the `${OLLAMA_API_KEY:?...}`
expansion in place); phosphor expands shell variables at config load.
Arm configs with literal keys are gitignored — commit only the templates.

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

## Anti-cheat egress proxy (optional, host-side)

Hosted-model runs get agent egress (`network_mode = "public"`), which also
lets a reward-seeking agent fetch the reference solution from GitHub. To
block this, run mitmproxy on the host with the kit's blocklist addon and
route the agent container through it:

```bash
mitmdump --mode regular --listen-host 0.0.0.0 --listen-port 8080 \
    -s benchmarks/deepswe1.1/tools/mitm_blocklist.py
HTTPS_PROXY=http://localhost:8080 benchmarks/deepswe1.1/run.sh
```

The addon (`tools/mitm_blocklist.py`) 403s github.com (and raw/media CDN
variants), gitlab.com, bitbucket.org, sourcegraph.com, grep.app,
searchcode.com, and live issue trackers, while package registries
(pypi, npm, proxy.golang.org, crates.io) and the model API stay open. The
allow/blocked lists are plain tuples at the top of the file. mitmproxy's
built-in `--set block_list=':~d github\.com$:403'` flags work too; the
addon exists so the policy is versioned with the kit rather than retyped
into a shell.

- `run.sh` detects host-exported `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`
  and forwards them into the agent container. A host-side loopback address
  is rewritten to `host.docker.internal` automatically; on macOS/Windows
  Docker Desktop that name resolves, but on native Linux Docker Engine it
  needs `extra_hosts: host.docker.internal:host-gateway` on the container,
  which Pier does not add on its own. `run.sh` pre-checks resolution from
  inside a container and fails with this guidance instead of hanging
  mid-run; the portable fallback is to point `HTTPS_PROXY` at the host's
  LAN IP.
- `agents/phosphor.py` uploads `~/.mitmproxy/mitmproxy-ca-cert.pem` into
  the container and trusts it system-wide, then exports the matching
  CA variables for `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `PIP_CERT`,
  `NODE_EXTRA_CA_CERTS`, `NPM_CONFIG_CAFILE`, `GIT_SSL_CAINFO`, and
  `DENO_TLS_CA_STORE`. This is fully conditional: hosts without a
  mitmproxy CA are unaffected and the proxy is simply not used.
- Pier's built-in `network_allowlist` only works for `no-network` tasks
  (allow-*only* filtering); a blocklist over an open network must ride on
  an external proxy, which is why this design is host-side.
- Treat a proxied run as experimental: validate the blocklist end-to-end
  (e.g. `curl` a blocked URL from inside a trial container) before quoting
  any score from such a run.

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
