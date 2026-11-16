#!/usr/bin/env python3
"""Pick a fixed SWE-bench subset for regression tracking.

Regression measurement depends on every run using the *same* instances, so
the subset is generated once, committed, and never regenerated. Selection is
sorted by instance id before sampling, so the same seed yields the same subset
even if the upstream dataset row order changes.

    python benchmarks/swebench/make_subset.py --size 40 \
        --out benchmarks/swebench/subsets/quick.txt
"""

from __future__ import annotations

import argparse
import random
from pathlib import Path

from run_swebench import load_instances


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--dataset", default="SWE-bench/SWE-bench_Verified")
    p.add_argument("--split", default="test")
    p.add_argument("--size", type=int, default=40)
    p.add_argument("--seed", type=int, default=0)
    p.add_argument("--out", type=Path, required=True)
    args = p.parse_args()

    instances = load_instances(args.dataset, args.split, None)
    ids = sorted(i["instance_id"] for i in instances)
    subset = sorted(random.Random(args.seed).sample(ids, min(args.size, len(ids))))

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text("\n".join(subset) + "\n")
    print(f"wrote {len(subset)} of {len(ids)} instances to {args.out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
