#!/usr/bin/env python3
"""Run Phosphor against SWE-bench instances and emit predictions.jsonl.

SWE-bench decouples inference from evaluation: the harness grades any agent
that emits ``{"instance_id", "model_name_or_path", "model_patch"}`` records.
This script is the inference side of that contract. For each instance it
restores the repository at its base commit, runs one unattended
``phosphor run --yolo`` turn, and records the resulting diff.

Grading is a separate command (see README.md):

    swebench eval verified -p predictions.jsonl --run-id phosphor-run1 -j 8
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import shutil
import sqlite3
import subprocess
import sys
import threading
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUT = Path(__file__).resolve().parent / "out"

# Credentials the agent's provider may need. Only variables that are actually
# set are forwarded, so a container inherits exactly the auth the host has.
# The list is the env vars catwalk's built-in provider configs reference;
# subscription providers (Charm Hyper, Copilot) authenticate via `phosphor
# login` and their credentials live in the data dir, which is isolated per
# instance, so they cannot be used for benchmarking.
FORWARDED_ENV = (
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
    "OPENAI_API_BASE",
    "AZURE_OPENAI_API_KEY",
    "GEMINI_API_KEY",
    "DEEPSEEK_API_KEY",
    "GROQ_API_KEY",
    "XAI_API_KEY",
    "MOONSHOT_API_KEY",
    "KIMI_CODING_API_KEY",
    "MINIMAX_API_KEY",
    "ZAI_API_KEY",
    "CEREBRAS_API_KEY",
    "FIREWORKS_API_KEY",
    "OPENROUTER_API_KEY",
    "HF_TOKEN",
    # Custom openai-compat servers (Ollama, vLLM, llama.cpp, LM Studio) take
    # whatever name the arm config uses. Harmless to forward: unset ones are
    # skipped, and they are only reachable via --allow-custom-provider.
    "LOCAL_API_KEY",
    "OLLAMA_API_KEY",
)

PROMPT_TEMPLATE = """{problem_statement}

Fix the repository so the issue above is resolved.

Rules:
- Modify the source files in place. Do not commit.
- Do not modify or add test files; the grading tests are applied separately.
- Make the smallest change that resolves the issue.
"""

_print_lock = threading.Lock()


def log(msg: str) -> None:
    with _print_lock:
        print(msg, flush=True)


def run(cmd: list[str], cwd: Path | None = None, env: dict | None = None, timeout: int | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        timeout=timeout,
        capture_output=True,
        text=True,
        # Agent output can contain progress-bar control bytes; strict
        # decoding raised "data must be str, not bytes" and lost the run.
        errors="replace",
    )


def run_logged(cmd: list[str], log_file: Path, timeout: int | None = None) -> tuple[bool, str]:
    """Run a command, writing output to disk even if it times out.

    subprocess.run buffers output in RAM and raises TimeoutExpired on top,
    which discards everything the agent printed before the timeout — the
    diagnosis for exactly the timed-out runs we care about. Returning partial
    output and logging it keeps that evidence.
    """
    try:
        proc = run(cmd, timeout=timeout)
        out = proc.stdout + proc.stderr
        timed_out = False
    except subprocess.TimeoutExpired as exc:
        out = (exc.stdout or "") + (exc.stderr or "")
        timed_out = True
    log_file.write_text(out)
    return timed_out, out


def load_instances(dataset: str, split: str, instance_ids: list[str] | None) -> list[dict]:
    from swebench.harness.utils import load_swebench_dataset

    return load_swebench_dataset(dataset, split, instance_ids or None)


def build_static_binary(out_dir: Path) -> Path:
    """Build a CGO-free phosphor binary that runs inside any instance image.

    CGO is required for tree-sitter structural search; disabling it costs the
    agent one search tool and buys a binary that runs in every container.
    """
    binary = out_dir / "phosphor-static"
    out_dir.mkdir(parents=True, exist_ok=True)
    env = {**os.environ, "CGO_ENABLED": "0"}
    proc = run(["go", "build", "-o", str(binary), "."], cwd=REPO_ROOT, env=env, timeout=1800)
    if proc.returncode != 0:
        raise SystemExit(f"failed to build phosphor:\n{proc.stderr}")
    return binary


def build_host_binary() -> Path:
    """Build phosphor for the host, leaving CGO at its default."""
    binary = REPO_ROOT / "phosphor"
    proc = run(["go", "build", "-o", str(binary), "."], cwd=REPO_ROOT, timeout=1800)
    if proc.returncode != 0:
        raise SystemExit(f"failed to build phosphor:\n{proc.stderr}")
    return binary


def build_prompt(instance: dict, with_hints: bool) -> str:
    text = instance["problem_statement"]
    if with_hints and instance.get("hints_text"):
        text = f"{text}\n\nHINTS:\n{instance['hints_text']}"
    return PROMPT_TEMPLATE.format(problem_statement=text)


def load_done(output: Path) -> set[str]:
    """Instance ids already recorded, so an interrupted run can resume.

    Records with an empty patch are not "done": they are interrupted or
    failed attempts, and a rerun should retry them rather than permanently
    marking the instance skipped.
    """
    if not output.exists():
        return set()
    done = set()
    for line in output.read_text().splitlines():
        if line.strip():
            try:
                record = json.loads(line)
            except json.JSONDecodeError:
                continue
            if record.get("instance_id") and record.get("model_patch", "").strip():
                done.add(record["instance_id"])
    return done


def append_prediction(output: Path, record: dict) -> None:
    with _print_lock, output.open("a") as fh:
        fh.write(json.dumps(record) + "\n")


def phosphor_argv(binary: Path, repo: Path, model: str | None, prompt_file: Path) -> list[str]:
    """The exact command the agent runs, quoted for re-use in both modes."""
    prompt = shlex.quote(prompt_file.read_text())
    argv = [str(binary), "--cwd", str(repo), "--yolo", "run", "--quiet"]
    if model:
        argv += ["--model", model]
    argv += [prompt]
    return argv


def git_output(repo: Path, args: list[str]) -> str:
    # Diffs can contain binary-ish content; strict decoding raised
    # "data must be str, not bytes" and lost the instance.
    return subprocess.run(
        ["git", *args], cwd=repo, capture_output=True, text=True, errors="replace"
    ).stdout


def capture_patch(repo: Path, mode: str) -> str:
    if mode == "all":
        subprocess.run(["git", "add", "-A"], cwd=repo, check=False)
        return git_output(repo, ["diff", "--cached"])
    return git_output(repo, ["diff"])


def reset_repo(repo: Path, base_commit: str) -> None:
    """Return the tree to the base commit, discarding any prior attempt."""
    run(["git", "checkout", base_commit], cwd=repo)
    run(["git", "reset", "--hard", base_commit], cwd=repo)
    run(["git", "clean", "-fd"], cwd=repo)


def prepare_local_repo(instance: dict, workspace: Path) -> Path:
    repo = workspace / instance["repo"].replace("/", "__")
    if not repo.exists():
        proc = run(
            ["git", "clone", f"https://github.com/{instance['repo']}", str(repo)],
            timeout=1800,
        )
        if proc.returncode != 0:
            raise RuntimeError(f"clone failed: {proc.stderr}")
    reset_repo(repo, instance["base_commit"])
    return repo


def phosphor_env(base: Path) -> dict[str, str]:
    """Host-side environment for one instance run.

    Global config, global data, and the provider cache each need their own
    root: catwalk writes providers.json to the cache dir and fails outright
    when it is unwritable, and the global config and data files are both
    named phosphor.json, so they must not share a directory.
    """
    env = {k: os.environ[k] for k in FORWARDED_ENV if os.environ.get(k)}
    env["PHOSPHOR_GLOBAL_CONFIG"] = str(base / "config")
    env["PHOSPHOR_GLOBAL_DATA"] = str(base / "data")
    env["PHOSPHOR_CACHE_DIR"] = str(base / "cache")
    env["PHOSPHOR_DISABLE_PROVIDER_AUTO_UPDATE"] = "1"
    return env


def inject_arm_config(config_dir: Path, args: argparse.Namespace) -> None:
    """Stage the arm's phosphor.json where Phosphor will read it.

    Phosphor's global config lives at $PHOSPHOR_GLOBAL_CONFIG/phosphor.json,
    so writing it there is how a benchmark arm toggles features such as
    agent.microcompaction. The workspace's own phosphor.json still merges on
    top of it, which is why the instance repos must stay unmodified.
    """
    config_dir.mkdir(parents=True, exist_ok=True)
    if args.config:
        shutil.copyfile(args.config, config_dir / "phosphor.json")


# Cache the revision: it is used to label every prediction and workdir.
_REVISION: str | None = None


def arm_label(args: argparse.Namespace) -> str:
    """Identifier recorded as model_name_or_path, so runs stay attributable."""
    label = f"phosphor-{phosphor_revision()}"
    return f"{label}-{args.arm}" if args.arm else label


def phosphor_revision() -> str:
    global _REVISION
    if _REVISION is None:
        proc = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"], cwd=REPO_ROOT, capture_output=True, text=True
        )
        _REVISION = proc.stdout.strip() or "unknown"
    return _REVISION


def run_local(instance: dict, args: argparse.Namespace, workdir: Path) -> tuple[bool, str]:
    repo = prepare_local_repo(instance, workdir / "repos")
    prompt_file = workdir / "prompt.txt"
    prompt_file.write_text(build_prompt(instance, args.with_hints))
    base = workdir / "phosphor"
    inject_arm_config(base / "config", args)
    env = {**os.environ, **phosphor_env(base)}
    timed_out, _ = run_logged(
        phosphor_argv(args.binary, repo, args.model, prompt_file),
        workdir / "agent.log",
        timeout=args.timeout,
    )
    return timed_out, capture_patch(repo, args.patch_mode)


def extract_token_usage(workdir: Path) -> dict[str, int]:
    """Pull per-instance token totals out of the agent's session database.

    The database lives in the container's checkout, which the host cannot
    read directly, so it is copied out of the scratch directory before the
    container is removed. Recording actual consumption matters for arm
    selection: local-model arms are free and API arms are not, and "should
    this arm run against an API provider" should be decided with numbers.
    """
    usage: dict[str, int] = {}
    dbs = list(workdir.glob("phosphor.db")) + list(workdir.glob("*/.phosphor/phosphor.db"))
    for db in dbs:
        try:
            conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
            row = conn.execute(
                "select coalesce(sum(prompt_tokens), 0), coalesce(sum(completion_tokens), 0), count(*) "
                "from token_usage"
            ).fetchone()
            conn.close()
            usage["prompt_tokens"] = usage.get("prompt_tokens", 0) + int(row[0])
            usage["completion_tokens"] = usage.get("completion_tokens", 0) + int(row[1])
            usage["model_calls"] = usage.get("model_calls", 0) + int(row[2])
        except Exception:  # noqa: BLE001 - missing db is not worth failing the sweep
            continue
    return usage


def aggregate_usage(out_dir: Path) -> dict[str, int]:
    """Sum the per-instance usage sidecars recorded during this run."""
    totals: dict[str, int] = {}
    for sidecar in out_dir.glob("*/*/*/usage.json"):
        try:
            usage = json.loads(sidecar.read_text())
        except (json.JSONDecodeError, OSError):
            continue
        for key, value in usage.items():
            totals[key] = totals.get(key, 0) + int(value)
    return totals


def run_docker(instance: dict, args: argparse.Namespace, workdir: Path) -> tuple[bool, str]:
    image = instance.get("image") or args.image
    if not image:
        raise RuntimeError(f"{instance['instance_id']}: dataset has no image field and no --image given")
    repo_dir = args.repo_dir
    prompt_file = workdir / "prompt.txt"
    prompt_file.write_text(build_prompt(instance, args.with_hints))
    inject_arm_config(workdir / "phosphor" / "config", args)
    patch_file = workdir / "model.patch"
    agent_log = workdir / "agent.log"

    # The binary and the prompt travel in as bind mounts so no per-instance
    # image has to be built; the repo already lives in the instance image.
    # The prompt is fed via stdin, not argv: bash splits an unquoted
    # substitution into words, and markdown rules in the problem statement
    # arrive as flags — "Bad flag syntax: -----".
    agent = ["phosphor", "--cwd", repo_dir, "--yolo", "run", "--quiet", "<", "/work/prompt.txt"]
    if args.model:
        agent += ["--model", args.model]

    stage = "git add -A && git diff --cached" if args.patch_mode == "all" else "git diff"
    # The agent step is `|| true`: a timeout or a crashed turn must still
    # produce a (possibly empty) patch, so the harness records a miss rather
    # than erroring the whole instance out.
    script = "\n".join(
        [
            "set -u",
            f"cd {repo_dir}",
            f"git checkout {instance['base_commit']}",
            "git reset --hard HEAD",
            "git clean -fd",
            # Phosphor auto-starts LSP servers found on PATH; the official
            # images ship neither, so install pyright best-effort when
            # asked. Failures degrade to a no-LSP run, never a failed
            # instance.
            "( test -f pyproject.toml || test -f setup.py || test -f setup.cfg ) "
            "&& ! command -v pyright-langserver >/dev/null "
            "&& pip install --quiet --disable-pip-version-check pyright || true" if args.lsp else "true",
            f"{' '.join(agent)} || true",
            # Copy the session database out before the container is removed;
            # it is the only record of what the agent spent on this instance.
            f"cp -f {repo_dir}/.phosphor/phosphor.db /work/ 2>/dev/null || true",
            f"{stage} > /work/model.patch",
        ]
    )

    docker_cmd = [
        "docker",
        "run",
        "--rm",
        "--network",
        "host",
        "-v",
        f"{args.static_binary}:/usr/local/bin/phosphor:ro",
        "-v",
        f"{workdir}:/work",
        "-e",
        "PHOSPHOR_GLOBAL_CONFIG=/work/phosphor/config",
        "-e",
        "PHOSPHOR_GLOBAL_DATA=/work/phosphor/data",
        "-e",
        "PHOSPHOR_CACHE_DIR=/work/phosphor/cache",
        "-e",
        "PHOSPHOR_DISABLE_PROVIDER_AUTO_UPDATE=1",
    ]
    for key in FORWARDED_ENV:
        if os.environ.get(key):
            docker_cmd += ["-e", key]
    docker_cmd += [image, "bash", "-lc", script]

    timed_out, _ = run_logged(docker_cmd, agent_log, timeout=args.timeout + 300)
    if patch_file.exists():
        return timed_out, patch_file.read_text()
    return timed_out, ""


def process(instance: dict, args: argparse.Namespace, output: Path, trial: int) -> None:
    instance_id = instance["instance_id"]
    workdir = args.out / arm_label(args) / f"trial_{trial}" / instance_id
    workdir.mkdir(parents=True, exist_ok=True)
    try:
        timed_out, patch = run_docker(instance, args, workdir) if args.docker else run_local(instance, args, workdir)
    except Exception as exc:  # noqa: BLE001 - one bad instance must not stop the sweep
        log(f"[{instance_id}] trial {trial}: error: {exc}")
        timed_out, patch = False, ""

    if timed_out:
        log(f"[{instance_id}] trial {trial}: timed out after {args.timeout}s")
    if not patch.strip():
        log(f"[{instance_id}] trial {trial}: empty patch (SWE-bench will skip this record)")
    else:
        log(f"[{instance_id}] trial {trial}: patch: {len(patch)} bytes")

    usage = extract_token_usage(workdir)
    if usage:
        (workdir / "usage.json").write_text(json.dumps(usage))
        log(f"[{instance_id}] trial {trial}: {usage['model_calls']} calls, "
            f"{usage['prompt_tokens']} prompt / {usage['completion_tokens']} completion tokens")

    if not patch.strip() and (timed_out or not (workdir / "model.patch").exists()):
        # Harness-induced miss (timeout, interrupted, or crashed run): leave
        # no record so the instance is retried, rather than letting an empty
        # patch stand in as an agent failure.
        return

    append_prediction(
        output,
        {
            "instance_id": instance_id,
            "model_name_or_path": arm_label(args),
            "model_patch": patch,
        },
    )


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--dataset", default="SWE-bench/SWE-bench_Verified", help="Hugging Face dataset name")
    p.add_argument("--split", default="test")
    p.add_argument("--instance-id", action="append", default=[], help="Limit to these instance ids (repeatable)")
    p.add_argument("--max-instances", type=int, default=0, help="Cap the number of instances (0 = all)")
    p.add_argument("--output", type=Path, default=None, help="Predictions file (default out/predictions.jsonl)")
    p.add_argument("--model", default=None, help="Phosphor model, e.g. anthropic/claude-sonnet-4-5")
    p.add_argument("--workers", type=int, default=1)
    p.add_argument("--timeout", type=int, default=1800, help="Per-instance agent timeout in seconds")
    p.add_argument("--with-hints", action="store_true", help="Append the dataset's hints_text to the prompt")
    p.add_argument("--patch-mode", choices=("tracked", "all"), default="tracked",
                   help="'tracked' diffs tracked files only (matches gold patches); "
                        "'all' also ships files the agent created")
    p.add_argument("--docker", action="store_true",
                   help="Run the agent inside the official instance image (recommended)")
    p.add_argument("--image", default=None, help="Override the instance image")
    p.add_argument("--repo-dir", default="/testbed", help="Repository path inside the instance image")
    p.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Scratch directory for logs and binaries")
    p.add_argument("--skip-build", action="store_true", help="Use an existing binary instead of compiling")
    p.add_argument("--phosphor-bin", default=None,
                   help="Binary for host-mode runs (--skip-build only); defaults to the repo binary or PATH")
    p.add_argument("--phosphor-static", default=None,
                   help="Pre-built static phosphor binary to mount into containers (--skip-build only)")
    p.add_argument("--arm", default=None,
                   help="Label for this configuration arm; namespaces outputs and grading logs")
    p.add_argument("--config", default=None,
                   help="phosphor.json to install as the global config for every instance (feature A/B)")
    p.add_argument("--trials", type=int, default=1,
                   help="Repeat every instance N times; each trial is a separate predictions file")
    p.add_argument("--instance-file", default=None,
                   help="File of instance ids, one per line (a pinned regression subset)")
    p.add_argument("--allow-custom-provider", action="store_true",
                   help="Permit an arm config that declares custom openai-compat providers")
    p.add_argument("--lsp", action="store_true",
                   help="Install pyright into the instance container so Phosphor's "
                        "auto-LSP starts it (Python-only benchmark; best-effort)")
    return p.parse_args()


def predictions_path_for(base: Path, args: argparse.Namespace, trial: int, label: str | None = None) -> Path:
    """Per-arm, per-trial file name.

    The label carries the Phosphor revision, so a regression run against a new
    commit cannot overwrite (or resume from) the predictions of an older one.
    The harness also collapses duplicate instance_ids in one file (last wins),
    so every trial has to be graded as its own file with its own run_id.
    """
    suffix = f".{label or arm_label(args)}"
    suffix += f".t{trial}" if trial > 1 else ""
    return base.with_name(f"{base.stem}{suffix}{base.suffix}")


def harvest_orphans(args: argparse.Namespace) -> None:
    """Record patches left behind by a driver that died mid-sweep.

    The container writes model.patch itself, so a killed driver leaves
    completed agent work on disk and nothing on the host knows about it.
    Folding those patches into the predictions file keeps an interrupted
    sweep from silently discarding it, and load_done() then skips the
    instances instead of paying for them twice.
    """
    for patch_file in sorted(args.out.glob("*/*/*/model.patch")):
        instance_id = patch_file.parent.name
        trial_dir = patch_file.parents[1].name
        label = patch_file.parents[2].name
        if not trial_dir.startswith("trial_") or not label or label == args.out.name:
            continue
        try:
            trial = int(trial_dir.removeprefix("trial_"))
        except ValueError:
            continue
        patch = patch_file.read_text()
        if not patch.strip():
            continue
        # Only harvest artifacts produced by this arm: patches left under a
        # different arm label were mislabeled at launch time and belong to
        # that arm's own predictions file.
        if label != arm_label(args):
            continue

        output = predictions_path_for(args.output, args, trial, label=label)
        if instance_id in load_done(output):
            continue
        output.parent.mkdir(parents=True, exist_ok=True)
        append_prediction(output, {"instance_id": instance_id, "model_name_or_path": label, "model_patch": patch})
        log(f"[{instance_id}] trial {trial}: harvested orphaned patch ({len(patch)} bytes) into {output.name}")


def validate_arm(args: argparse.Namespace) -> None:
    if not args.config:
        return
    path = Path(args.config)
    if not path.is_file():
        raise SystemExit(f"--config {path} does not exist")
    try:
        cfg = json.loads(path.read_text())
    except json.JSONDecodeError as exc:
        raise SystemExit(f"--config {path} is not valid JSON: {exc}")
    # The driver forwards no provider credentials beyond built-in env vars, so
    # a custom openai-compat server is only reachable if the operator says so.
    if cfg.get("providers") and not args.allow_custom_provider:
        raise SystemExit(
            f"--config {path} declares custom providers; pass --allow-custom-provider "
            "to confirm the credentials are forwarded via the environment"
        )


def main() -> int:
    args = parse_args()
    args.out = args.out.resolve()  # container bind mounts need absolute paths
    args.out.mkdir(parents=True, exist_ok=True)
    if args.output is None:
        args.output = args.out / "predictions.jsonl"
    if args.trials < 1:
        raise SystemExit("--trials must be >= 1")
    validate_arm(args)
    if not args.model:
        log("warning: --model not set; each instance falls back to the configured default model")

    instance_ids = list(args.instance_id)
    if args.instance_file:
        instance_ids += [
            line.strip() for line in Path(args.instance_file).read_text().splitlines() if line.strip()
        ]

    instances = load_instances(args.dataset, args.split, instance_ids)
    if args.max_instances:
        instances = instances[: args.max_instances]

    if args.skip_build:
        # Resolve against PATH/repo root now: the driver later runs with cwd
        # set to each instance's clone, so a bare relative name would not find
        # the binary.
        if args.phosphor_bin:
            args.binary = Path(args.phosphor_bin).resolve()
        else:
            args.binary = Path(shutil.which("phosphor") or (REPO_ROOT / "phosphor")).resolve()
        args.static_binary = Path(args.phosphor_static).resolve() if args.phosphor_static else None
        if args.docker and args.static_binary is None:
            raise SystemExit("--docker with --skip-build also needs --phosphor-static <path>")
    else:
        args.binary = build_host_binary()
        args.static_binary = build_static_binary(args.out) if args.docker else None

    harvest_orphans(args)

    for trial in range(1, args.trials + 1):
        output = predictions_path_for(args.output, args, trial)
        done = load_done(output)
        todo = [i for i in instances if i["instance_id"] not in done]
        log(f"trial {trial}/{args.trials}: {len(instances)} instances, "
            f"{len(done)} already recorded, {len(todo)} to run")

        with ThreadPoolExecutor(max_workers=args.workers) as pool:
            try:
                list(pool.map(lambda inst: process(inst, args, output, trial), todo))
            except KeyboardInterrupt:
                # Ctrl-C must not discard completed work: containers keep
                # running and write their patches, and the next launch
                # harvests them. Exit cleanly so the predictions file stays
                # valid JSONL.
                log("interrupted — in-flight containers will finish; rerun to harvest and resume")
                return 130
        log(f"wrote {output}")

    (args.out / f"manifest.{arm_label(args)}.json").write_text(
        json.dumps(
            {
                "label": arm_label(args),
                "arm": args.arm,
                "config": str(Path(args.config).resolve()) if args.config else None,
                "model": args.model,
                "dataset": args.dataset,
                "split": args.split,
                "trials": args.trials,
                "docker": args.docker,
                "patch_mode": args.patch_mode,
                "phosphor_revision": phosphor_revision(),
                "instances": sorted(i["instance_id"] for i in instances),
                "token_usage": aggregate_usage(args.out),
            },
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
