#!/usr/bin/env python3
"""Check this machine is ready to benchmark, before any API calls are spent.

    python benchmarks/swebench/preflight.py

Exits non-zero if any required check fails, so it can gate CI too.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

MIN_DISK_GB = 120
MIN_RAM_GB = 16
MIN_CPUS = 8

# The config the driver installs as the global config for every instance,
# checked in this order.
BENCHMARK_CONFIGS = (
    Path("benchmarks/swebench/phosphor.json"),
    Path("benchmarks/swebench/arms/base.json"),
)

PROVIDER_KEYS = (
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
    "GEMINI_API_KEY",
    "AZURE_OPENAI_API_KEY",
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
    "PHOSPHOR_HYPER_API_KEY",
)


def docker_ok() -> tuple[bool, str]:
    if not shutil.which("docker"):
        return False, "docker is not installed"
    proc = subprocess.run(
        ["docker", "info", "--format", "{{.NCPU}} {{.MemTotal}}"], capture_output=True, text=True
    )
    if proc.returncode != 0:
        return False, "docker daemon is not reachable (is it running? does your user need sudo?)"
    cpus, mem_bytes = proc.stdout.split()
    # `docker info` reports MemTotal in bytes, not KB.
    cpus, ram_gb = int(cpus), int(mem_bytes) // (1024**3)
    if cpus < MIN_CPUS or ram_gb < MIN_RAM_GB:
        return False, f"{cpus} cpus / {ram_gb} GB ram (need {MIN_CPUS} cpus / {MIN_RAM_GB} GB)"
    return True, f"daemon up, {cpus} cpus, {ram_gb} GB ram"


def disk_ok() -> tuple[bool, str]:
    free_gb = shutil.disk_usage(Path.cwd()).free // (1024**3)
    return free_gb >= MIN_DISK_GB, f"{free_gb} GB free (need {MIN_DISK_GB})"


def swebench_importable() -> tuple[bool, str]:
    try:
        from swebench.harness.utils import load_swebench_dataset  # noqa: F401
    except ImportError as exc:
        return False, f"cannot import swebench ({exc}); install it (see README 'Setup')"
    return True, "swebench importable"


def provider_key() -> tuple[bool, str]:
    cfg_path = benchmark_config()
    if cfg_path is not None:
        try:
            cfg = json.loads(cfg_path.read_text())
        except (json.JSONDecodeError, OSError):
            cfg = {}
        if cfg.get("providers"):
            # A custom openai-compat provider is self-describing: the arm
            # config carries base_url, and the server may not check a key.
            return True, f"custom provider declared in {cfg_path}"
    present = [k for k in PROVIDER_KEYS if os.environ.get(k)]
    if not present:
        return False, "no provider API key set; export one of " + ", ".join(PROVIDER_KEYS)
    return True, "found " + ", ".join(present)


def benchmark_config() -> Path | None:
    """The config file the driver will install for every instance."""
    for candidate in BENCHMARK_CONFIGS:
        if candidate.is_file():
            return candidate
    arms = sorted(Path("benchmarks/swebench/arms").glob("*.json"))
    return arms[0] if arms else None


def headless_safe() -> tuple[bool, str]:
    """Two settings decide whether an unattended run can strand.

    `security.disable_yolo` pins yolo off, so a permission prompt has no
    answerer. Sandbox escalation prompts *bypass* yolo by design, so a
    sandboxed bash child that hits a denial has no answerer either.
    """
    cfg_path = benchmark_config()
    if cfg_path is None:
        return True, "sandbox active; yolo available"
    try:
        cfg = json.loads(cfg_path.read_text())
    except (json.JSONDecodeError, OSError) as exc:
        return False, f"{cfg_path} unreadable: {exc}"
    if (cfg.get("security") or {}).get("disable_yolo"):
        return False, f"{cfg_path} sets security.disable_yolo; unattended runs would hang"
    sandbox_mode = ((cfg.get("tools") or {}).get("bash") or {}).get("sandbox_mode", "workspace")
    if sandbox_mode == "off":
        # Legitimate for benchmarking — the instance container is the
        # boundary — but it is a deliberate weakening, so say it out loud.
        return True, "sandbox_mode=off: benchmark runs unconfined inside the instance container"
    return True, (
        f"sandbox_mode={sandbox_mode}: if the agent escalates past a denial the run will "
        "hang, because escalation prompts bypass yolo. Leave as-is unless it happens."
    )


def main() -> int:
    checks = [
        ("docker", docker_ok()),
        ("disk", disk_ok()),
        ("swebench", swebench_importable()),
        ("provider key", provider_key()),
        ("headless safety", headless_safe()),
    ]
    failed = [name for name, (ok, _) in checks if not ok]
    for name, (ok, detail) in checks:
        print(f"[{'PASS' if ok else 'FAIL'}] {name}: {detail}")
    if failed:
        print(f"\n{len(failed)} check(s) failed: {', '.join(failed)}")
        return 1
    print("\nready")
    return 0


if __name__ == "__main__":
    sys.exit(main())
