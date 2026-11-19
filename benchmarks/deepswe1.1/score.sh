#!/usr/bin/env bash
# Aggregate DeepSWE v1.1 trial results into a pass rate.
#
# Usage: benchmarks/deepswe1.1/score.sh [jobs-dir]   (default: ./jobs)
#
# Reads every trial's verifier/reward.json and reports resolved / submitted.
# Infra failures (missing reward.json, non-zero verifier error) are listed
# separately — a score is only meaningful when the infra-failure count is 0.
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
JOBS_DIR="${1:-$BENCH_DIR/jobs}"

python3 - "$JOBS_DIR" <<'EOF'
import json
import sys
from pathlib import Path

jobs_dir = Path(sys.argv[1])
rewards = sorted(jobs_dir.glob("**/verifier/reward.json"))

resolved = submitted = 0
failed = []
missing = []
for path in rewards:
    try:
        reward = json.loads(path.read_text())
    except (json.JSONDecodeError, OSError):
        missing.append(str(path.parent.parent))
        continue
    submitted += 1
    value = reward.get("reward")
    if isinstance(value, (int, float)) and value >= 1:
        resolved += 1
    else:
        failed.append(path.parents[1].name)

trials = sorted({p.parents[1] for p in rewards}) if rewards else []
unscored = [str(t) for t in trials if not (t / "verifier" / "reward.json").exists()]

print(f"submitted: {submitted}")
print(f"resolved:  {resolved}")
if submitted:
    print(f"pass rate: {resolved / submitted:.1%}")
if failed:
    print(f"unresolved: {', '.join(failed)}")
if unscored:
    print(f"!! {len(unscored)} trial(s) without verifier output (infra failures):")
    for t in unscored[:10]:
        print(f"   {t}")
EOF
