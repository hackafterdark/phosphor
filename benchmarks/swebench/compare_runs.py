#!/usr/bin/env python3
"""Compare graded SWE-bench runs so a code or config change can be judged.

Point it at the grading run ids produced by `swebench eval`. Each side may be
given more than once when the arm was run with `--trials N`; repeating an arm
turns a single noisy measurement into pass@1 and pass@k.

    python benchmarks/swebench/compare_runs.py \
        --baseline  phosphor-a3f2c1d-t1 --baseline phosphor-a3f2c1d-t2 \
        --candidate phosphor-b8d4e2f-microcompaction-t1

Run ids are resolved against `logs/evaluation/` by default, or pass
`--logs-dir` to point somewhere else.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

RESULT_FILE = "results.json"


def load_run(run_id: str, logs_dir: Path) -> dict:
    path = Path(run_id)
    if not path.is_file():
        path = logs_dir / run_id / RESULT_FILE
    if not path.is_file():
        raise SystemExit(f"no {RESULT_FILE} for run {run_id!r} (looked in {logs_dir})")
    return json.loads(path.read_text())


def summarise(run_ids: list[str], logs_dir: Path) -> dict:
    """Aggregate trials of one arm into pass@1 (mean) and pass@k (any)."""
    trials = [load_run(r, logs_dir) for r in run_ids]
    resolved_sets = [set(t.get("resolved_ids", [])) for t in trials]
    submitted = set(trials[0].get("submitted_ids", []))
    # An unparseable test log or a corrupt report tells you nothing about the
    # agent, so those instances are excluded rather than scored as failures.
    # An empty patch is different: the agent produced nothing, which is a
    # genuine miss and stays in the denominator.
    noise = set()
    for t in trials:
        noise |= set(t.get("infra_failure_ids", []))
        noise |= set(t.get("error_ids", []))

    rates = [len(r) / len(submitted) * 100 for r in resolved_sets if submitted]
    pass_at_k = set().union(*resolved_sets) if resolved_sets else set()
    return {
        "trials": len(trials),
        "submitted": len(submitted),
        "attempts": submitted - noise,
        "noise": noise,
        "resolved_sets": resolved_sets,
        "pass_at_1": sum(rates) / len(rates) if rates else 0.0,
        "pass_at_k": len(pass_at_k),
    }


def verdict(label: str, stats: dict) -> str:
    k = f"pass@{stats['trials']}" if stats["trials"] > 1 else "resolved"
    return f"{label}: {k} {stats['pass_at_k']}/{stats['submitted']} ({stats['pass_at_1']:.1f}% mean)"


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--baseline", action="append", required=True, metavar="RUN_ID")
    p.add_argument("--candidate", action="append", required=True, metavar="RUN_ID")
    p.add_argument("--logs-dir", type=Path, default=Path("logs/evaluation"))
    p.add_argument("--limit", type=int, default=30, help="Max instance ids to print per list")
    p.add_argument("--verbose", "-v", action="store_true", help="Also list instances neither side resolved")
    args = p.parse_args()

    base = summarise(args.baseline, args.logs_dir)
    cand = summarise(args.candidate, args.logs_dir)
    if base["trials"] != cand["trials"]:
        raise SystemExit(
            f"trial counts differ ({base['trials']} vs {cand['trials']}); pass@k is not "
            "comparable across unequal trial counts. Either pass the same number of run ids "
            "on both sides, or compare pass@1 by passing one run id per side."
        )

    # Only instances both sides genuinely attempted are comparable.
    base_any = set().union(*base["resolved_sets"])
    cand_any = set().union(*cand["resolved_sets"])
    comparable = base["attempts"] & cand["attempts"]

    fixed = sorted((cand_any - base_any) & comparable)
    regressed = sorted((base_any - cand_any) & comparable)
    stable_fail = sorted(comparable - base_any - cand_any)

    print(verdict("baseline ", base))
    print(verdict("candidate", cand))
    delta = cand["pass_at_k"] - base["pass_at_k"]
    print(f"\ndelta: {delta:+d} resolved")
    if base["noise"] or cand["noise"]:
        print(f"excluded as non-capability: {len(base['noise'])} baseline, "
              f"{len(cand['noise'])} candidate (infra failure or grading error)")

    def show(title: str, ids: list[str]) -> None:
        print(f"\n{title} ({len(ids)}):")
        for i in ids[: args.limit]:
            print(f"  {i}")
        if len(ids) > args.limit:
            print(f"  ... and {len(ids) - args.limit} more")

    show("fixed by candidate", fixed)
    show("regressed against baseline", regressed)
    if args.verbose:
        show("unresolved by both", stable_fail)

    if abs(delta) <= 1 and (fixed or regressed):
        print("\nNarrow margin: on a subset this size a 1-instance difference is within")
        print("run-to-run noise. Re-run with --trials 3 on both arms before concluding.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
