# SWE-bench

`swebench` grades an agent by applying its patch inside a pinned Docker image
and running the instance's own tests. Phosphor only has to produce the patch,
so the whole integration is one driver script: `run_swebench.py`.

## Setup

Four things have to be true. `preflight.py` checks all of them, so run it
first and you will not discover a missing piece in the middle of a sweep:

```bash
python benchmarks/swebench/preflight.py
```

1. **Docker** is installed and the daemon is reachable. Evaluation runs in the
   official per-instance images.
2. **Disk, RAM, cores**: ≥120 GB free, ≥16 GB RAM, ≥8 cores, x86_64.
3. **SWE-bench is installed** in the Python environment that runs the driver:

   ```bash
   git clone https://github.com/SWE-bench/SWE-bench.git
   cd SWE-bench && pip install -e .
   ```

   Prefer a virtualenv if your distribution's Python is externally managed
   (`python3 -m venv venv && venv/bin/pip install -e .`). The driver and
   `swebench eval` must both run under that venv's interpreter — a bare
   `swebench` on `PATH` will not be found, so invoke the venv binary by path
   (`.tmp/v1/bin/swebench` below) or activate the venv first.
4. **A provider API key is exported** in the environment. Phosphor activates a
   built-in provider as soon as its key is present — no `phosphor login` and no
   config file needed:

   ```bash
   export ANTHROPIC_API_KEY=sk-ant-...
   ```

   **Which provider:** any built-in provider works — `ANTHROPIC_API_KEY`,
   `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `DEEPSEEK_API_KEY`,
   and ~35 others. Pick one and pin it: benchmark scores are only comparable
   when the model is fixed, so use the same `--model` for baseline and
   candidate arms. Prefer a model with strong, reliable tool calling; if the
   model flubs tool calls, you will measure the model, not Phosphor.

   Subscription providers (Charm Hyper, GitHub Copilot) authenticate through
   `phosphor login`, whose credentials live in the data directory — which is
   isolated per instance — so they cannot be used for benchmarking.

   **Local models** also work, via an `openai-compat` provider in the arm
   config. Pass `--allow-custom-provider` to opt in, since the default driver
   refuses configs that declare custom providers:

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
     }
   }
   ```

   ```bash
   task swebench -- --docker --allow-custom-provider --config arms/local.json \
       --model local/qwen3-32b --workers 4
   ```

   `arms/local.json` is gitignored (machine-specific endpoint). Copy
   `arms/local.example.json` and adjust. Arm values go through shell
   expansion (`$VAR`, `${VAR:?msg}`, `$(cmd)`), so credentials can stay in
   the environment instead of the file.

**LSP:** Verified is Python-only, so `--lsp` installs pyright into the
instance container before the agent turn; Phosphor's auto-LSP starts it
automatically once the binary is on `PATH`. Best-effort — an install failure
degrades that instance to a no-LSP run.

### Restricted environments

Sandboxes and CI often cannot write to `~/.cache` or `~/.local`. Relocate the
caches into the workspace and the whole flow works unchanged:

```bash
export UV_CACHE_DIR=$PWD/.tmp/uv-cache     # if you install with uv
export HF_HOME=$PWD/.tmp/hf                # dataset download cache
```

`HF_HOME` matters for more than disk space: `swebench eval` also downloads the
dataset, so grading fails with a `PermissionError` under `~/.cache` if you
forget it.

The driver rebuilds Phosphor on every run by default. Pass `--skip-build` to
use binaries you built yourself — required if you cannot overwrite the repo
binary while a Phosphor session is running. `--phosphor-bin <path>` covers
host mode; `--docker` additionally needs `--phosphor-static <path>`.

Do **not** build the container binary on the host. Host glibc is newer than
the instance images' (Ubuntu 22.04, glibc 2.35), so a host-built CGO binary
dies with `GLIBC_2.38 not found`, and `CGO_ENABLED=0` silently disables
tree-sitter, taking `structural_search` away from the agent. Build it in an
older-glibc container instead — Debian 11 (glibc 2.31) runs on every image:

```bash
docker run --rm -v .:/src -e GOTOOLCHAIN=auto \
    -e GOMODCACHE=/src/.tmp/gomodcache -e GOPATH=/src/.tmp/gopath \
    -e CGO_ENABLED=1 golang:1.23-bullseye \
    bash -c 'cd /src && go build -buildvcs=false -o .tmp/phosphor-cgo .'
```

Then pass `--phosphor-static .tmp/phosphor-cgo` and the agent gets
`structural_search` inside the containers.

Phosphor's own caches are handled by the driver — it sets
`PHOSPHOR_GLOBAL_CONFIG`, `PHOSPHOR_GLOBAL_DATA`, `PHOSPHOR_CACHE_DIR` and
`PHOSPHOR_DISABLE_PROVIDER_AUTO_UPDATE=1` per instance, so a benchmark run
never touches your real config or provider cache. The corollary is that a run
inherits **none** of your personal config unless you pass `--config`: results
cannot depend on your machine, and sandbox defaults are always on.

### Check the dataset is reachable

```bash
python benchmarks/swebench/make_subset.py --size 40 \
    --out benchmarks/swebench/subsets/quick.txt
```

This downloads the dataset, so it doubles as a connectivity test. It prints
`wrote 40 of 500 instances`, confirming you have access to the full pool.

If you are on ARM (Apple Silicon), images must be built locally with
`--task-repo`; see the SWE-bench Docker guide.

## Run it

Run everything from the **repository root** — `-p`, `--out`, and `HF_HOME` are
all relative, and the driver's bind mounts need absolute paths. Long sweeps
should run inside `tmux` or `nohup`: if the driver process dies, the container
finishes on its own and its patch is harvested on the next launch, but nobody
is left to enforce the per-instance timeout.

`.tmp/v1` below is the workspace-local venv from Setup; substitute your own
interpreter if SWE-bench lives elsewhere.

```bash
cd /path/to/phosphor
export HF_HOME=$PWD/.tmp/hf

# 0. Check the machine before spending anything.
.tmp/v1/bin/python3 benchmarks/swebench/preflight.py

# 1. Sanity-check the evaluation setup with gold patches (no agent involved).
#    This is the one command that tells you whether your Docker setup is sound
#    before you spend API budget.
.tmp/v1/bin/swebench eval verified --gold -i sympy__sympy-20590 \
    -r validate-gold -j 1

# 2. Run Phosphor over one instance.
task swebench -- --docker --model anthropic/claude-sonnet-4-5 \
    --instance-id sympy__sympy-20590 --workers 1

# 3. Grade the predictions (the file is named after the Phosphor revision).
.tmp/v1/bin/swebench eval verified \
    -p benchmarks/swebench/out/predictions.$(git rev-parse --short HEAD).jsonl \
    --run-id phosphor-run1 -j 8

# 4. Read the score.
.tmp/v1/bin/swebench report phosphor-run1 -d verified
```

For a pinned subset, or a local model server:

```bash
# Pinned 40-instance regression subset.
task swebench -- --docker --model anthropic/claude-sonnet-4-5 \
    --instance-file benchmarks/swebench/subsets/quick.txt --trials 3 --workers 4

# Local openai-compat server: --allow-custom-provider opts in to the arm config.
task swebench -- --docker --allow-custom-provider \
    --config benchmarks/swebench/arms/local.json \
    --skip-build --phosphor-static .tmp/phosphor-static \
    --instance-file benchmarks/swebench/subsets/quick.txt --trials 3 --workers 4
```

Start with one instance (`--instance-id`) and `--workers 1`. Only scale up
once a single instance goes green end to end.

Useful flags:

| Flag | Effect |
| --- | --- |
| `--workers N` | Instances in parallel; each gets its own container and data dir. |
| `--max-instances N` | Cap the sweep for a quick smoke test. |
| `--with-hints` | Append the dataset's `hints_text` to the prompt. |
| `--patch-mode all` | Also ship files the agent created (`tracked` is the default and matches gold-patch semantics). |
| `--timeout N` | Per-instance agent timeout, default 1800 s. |
| `--skip-build` | Reuse an existing binary instead of compiling. |
| `--phosphor-bin <path>` | Host-mode binary to use with `--skip-build`. |
| `--phosphor-static <path>` | Static (CGO-free) binary to mount into containers with `--skip-build`; required for `--docker`. |
| `--arm <label>` | Namespace outputs and `model_name_or_path` for A/B runs. |
| `--trials N` | Repeat every instance; each trial grades as its own file. |

Re-running the same command skips instances already present in
`predictions.<phosphor-sha>.jsonl`, so an interrupted sweep resumes where it
stopped. If the driver itself dies mid-sweep, the completed patches are not
lost: the container writes `model.patch` into its scratch directory, and the
next run harvests any patch that was never recorded into the predictions file
instead of paying for that instance twice. Use a
fresh `--run-id` for every grading pass: SWE-bench caches reports on
`run_id` + `instance_id` and will happily re-report a stale result.

### What a running container looks like

`docker logs <container>` shows the driver's `git checkout`/`git reset`
preamble and then goes quiet — that is expected, not a hang. `phosphor run
--quiet` sends its spinner to stderr (and disables it when stderr is not a
TTY), prints only assistant text, and `git diff > /work/model.patch` is
redirected to a file. The next line is the agent's final answer.

To confirm progress while an instance is running, read the session database
the agent writes inside the container:

```bash
docker exec <container> python3 - <<'PY'
import sqlite3
c = sqlite3.connect("/testbed/.phosphor/phosphor.db")
print(c.execute("select count(*) from messages").fetchone())
print(c.execute("select max(created_at) from token_usage").fetchone())
PY
```

Fresh `token_usage` timestamps mean the agent is talking to the model. If
`git status` in `/testbed` also shows edits, the run is fine and you should
leave it alone until it exits.

## Two ways to run the agent

**`--docker` (recommended)** starts `phosphor` inside the official instance
image, so the agent sees exactly the environment the grader will use — the
right Python, the right dependencies, and tests it can actually run. The
binary is bind-mounted in. The driver's default build is `CGO_ENABLED=0`,
which silently drops `structural_search` (tree-sitter needs cgo) — for any run
whose score you intend to report, supply the container-built CGO binary from
the previous section via `--phosphor-static` instead. The repo path inside the
image defaults to `/testbed` and can be overridden with `--repo-dir`.

**Host mode (default)** clones the repository next to the current checkout and
runs the agent against it. Fast to iterate on, but the agent's environment is
whatever is on your machine, so scores are not comparable to published
results. Use it for prompt and agent-behaviour work, not for reporting.

## Permissions and the sandbox

These are two independent layers, and `--yolo` only covers one of them.

**Permissions** — `phosphor run` has no terminal to answer prompts, so any tool
call not covered by `permissions.allowed_tools` blocks until the process is
interrupted. The driver always passes `--yolo`. If your global config sets
`security.disable_yolo`, that override wins and unattended runs will hang —
clear it before benchmarking.

**The kernel sandbox** is a policy ceiling, not a permission gate, and it is
unaffected by `--yolo`. It stays on for benchmark runs — the default
`workspace` mode confines writes to the instance repo, which is exactly the
boundary you want, and it degrades gracefully rather than blocking when the
kernel cannot enforce a policy.

The one failure mode is escalation. If the agent hits a legitimate denial and
retries with `sandbox="escalate"`, that request goes through
`BypassShortcuts`, which deliberately ignores YOLO mode, tool allowlists, and
hook pre-approvals — it prompts the user per command. Headless, there is no
user, so the run hangs.

That is rare in practice: SWE-bench instances are graded on edits inside the
repo, and the images are pre-built, so the agent has little reason to reach
outside. Leave the sandbox on and run it. If a run does strand, allowlist the
specific toolchain rather than disabling the sandbox:

```json
{
  "tools": {
    "bash": {
      "auto_escalate_commands": ["pip", "conda", "pytest"]
    }
  }
}
```

`auto_escalate_commands` is argv-verified and operator-only — a workspace may
narrow the list but never widen it — so it grants the specific commands you
trust instead of switching the whole boundary off. `sandbox_mode: off` also
exists and is operator-only for the same reason, but reaching for it turns a
per-command grant into no containment at all; prefer the allowlist.

## Measuring Phosphor itself

Because the harness is fixed and only Phosphor changes, this is also a
regression suite for the agent's tools and loops. Pin everything else so the
code is the only variable:

```bash
# Baseline: the commit you are comparing against.
git checkout a3f2c1d
task swebench -- --docker --model anthropic/claude-sonnet-4-5 \
    --instance-file benchmarks/swebench/subsets/quick.txt --trials 3 --workers 4

# Candidate: the commit under test.
git checkout b8d4e2f
task swebench -- --docker --model anthropic/claude-sonnet-4-5 \
    --instance-file benchmarks/swebench/subsets/quick.txt --trials 3 --workers 4
```

Grade each trial, then compare. The driver names predictions
`predictions.<phosphor-sha>[-<arm>].t<n>.jsonl`, so each build keeps its own
predictions and a new commit can never overwrite or resume from an old one:

```bash
swebench eval verified -p benchmarks/swebench/out/predictions.a3f2c1d.t1.jsonl --run-id base-t1 -j 8
# … repeat for every trial of both sides …

python benchmarks/swebench/compare_runs.py \
    --baseline base-t1 --baseline base-t2 --baseline base-t3 \
    --candidate cand-t1 --candidate cand-t2 --candidate cand-t3
```

`compare_runs.py` prints pass@k, the instances the candidate fixed, and the
instances it regressed. It drops instances whose grading failed for
infrastructural reasons rather than scoring them against the agent, and it
refuses to compare unequal trial counts, because pass@k across 3 trials
against pass@1 always flatters the side with more attempts.

**Treat single-run differences of one or two instances as noise.** The agent
is stochastic; nothing in Phosphor can seed the model. The only way to tell a
real improvement from luck is to run the same subset three times per side and
compare pass@k.

Keep the subset pinned and small. SWE-bench Verified is 500 instances; a
40-instance subset you never change is worth far more for regression work than
an occasionally-rerun full set, because the *same* subset is what makes two
runs comparable.

```bash
python benchmarks/swebench/make_subset.py --size 40 \
    --out benchmarks/swebench/subsets/quick.txt
```

Commit the file. Regenerating it with a different seed silently invalidates
every comparison against earlier runs.

## A/B a configuration option

The same machinery measures a config toggle, with `--arm` to label the outputs
and `--config` to install a global `phosphor.json` for every instance:

```bash
task swebench -- --docker --arm microcompaction-off \
    --config benchmarks/swebench/arms/microcompaction-off.json \
    --instance-file benchmarks/swebench/subsets/quick.txt --trials 3

task swebench -- --docker --arm microcompaction-on \
    --config benchmarks/swebench/arms/microcompaction-on.json \
    --instance-file benchmarks/swebench/subsets/quick.txt --trials 3
```

Both arms run from the same commit, so `--arm` is the only difference.
`--arm` also namespaces `model_name_or_path`, so grading logs from the two
arms cannot overwrite each other, and each run writes
`out/manifest.<label>.json` recording the config, model, dataset, and Phosphor
revision that produced it.

Anything in `phosphor.json` can be an arm: `options.agent`, `tools.*`,
`permissions`, `lsp`. Three things to remember:

- **The arm file replaces the global config for the run**; there is no merge.
  Every setting the arms depend on must appear in both files, and neither arm
  should touch `tools.bash` — sandbox policy belongs to the operator, not to
  the experiment.
- Built-in providers still activate from environment API keys, so a minimal
  arm file is fine.
- The workspace's own `phosphor.json` still merges on top of the arm config.
  The instance repositories do not ship one, but a subset that does would
  silently change the arm.

## Reading the result

**The score is `resolved_instances / submitted_instances` in
`logs/evaluation/<run_id>/results.json`.** Read it alongside
`infra_failure_instances`: if that is not `0`, the environment — not the
agent — decided the run, and the number is meaningless.

```bash
.tmp/v1/bin/swebench report phosphor-run1 -d verified
```

| Field | Meaning |
| --- | --- |
| `resolved_instances` / `resolved_ids` | The score |
| `unresolved_instances` | A patch was produced and the tests still failed |
| `infra_failure_instances` | Container/environment failure — must be 0 |
| `empty_patch_instances` | Agent timed out or stalled; graded separately |
| `ambiguous_failure_ids` | The grader itself errored |

Two runs are only comparable when `infra_failure_instances` is 0 on both, and
a run over a handful of instances is a plumbing test, not a score — two
instances will look great (100%) while telling you nothing about capability.
Anything you intend to quote against other agents has to cover the full
dataset; the pinned 40-instance subset is for regression tracking against your
own history only.

Per-instance detail lives in
`logs/evaluation/<run_id>/<model_name_or_path>/<instance_id>/`:

- `report.json` — `FAIL_TO_PASS` is the fix itself, `PASS_TO_PASS` catches
  regressions introduced by the patch.
- `test_output.txt`, `patch.diff`, `run_instance.log`, `eval.sh` — the raw
  evidence for why an instance failed.

`report <run_id>` re-grades saved logs without starting containers, so a
verdict can be recomputed in seconds while debugging the log parser.

- A harness crash and an un-gradable instance are counted separately from
  test failures. Read `run_instance.log`, not just the summary.
- An empty `model_patch` is skipped, not graded. The driver logs
  `empty patch` when this happens; usually it means the agent timed out or
  the permission gate stopped it.

In `compare_runs.py`, an empty patch counts as a miss: the agent was given the
instance and produced nothing. That is different from an infra failure, which
is excluded from the comparison entirely.
