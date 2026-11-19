#!/usr/bin/env bash
# Materialize a run-ready copy of the DeepSWE v1.1 tasks.
#
# DeepSWE declares `[agent] network_mode = "no-network"`, which blocks the
# agent's calls to hosted model APIs (Ollama Cloud, OpenRouter, ...). The
# leaderboard runs the agent inside the sandbox on Modal where egress is
# allowed; for local Docker runs we patch the agent-scope network mode to
# "open". The verifier environment is untouched — it keeps its own isolated
# grading run either way.
#
# Usage: benchmarks/deepswe1.1/prepare_tasks.sh [src_tasks_dir] [dst_tasks_dir]
set -euo pipefail

SRC="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.tmp/deep-swe/tasks}"
DST="${2:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/tasks-open}"

[ -d "$SRC" ] || { echo "source tasks dir $SRC missing; run setup.sh first" >&2; exit 1; }

rm -rf "$DST"
cp -r "$SRC" "$DST"
n=0
while IFS= read -r -d '' f; do
    # Rewrite via Python: sed -i semantics differ between GNU and BSD sed,
    # and this keeps the patch portable to macOS hosts.
    if PY="$(command -v python3 || true)" && [ -n "$PY" ]; then
        "$PY" - "$f" >/dev/null <<'PYEOF'
import re
import sys

path = sys.argv[1]
text = open(path).read()
agent = re.search(r"^\[agent\].*?(?=^\[|\Z)", text, re.M | re.S)
if agent and 'network_mode = "no-network"' in agent.group(0):
    patched = agent.group(0).replace(
        'network_mode = "no-network"', 'network_mode = "public"'
    )
    open(path, "w").write(text[: agent.start()] + patched + text[agent.end() :])
    print("patched", path)
PYEOF
    else
        echo "python3 is required to patch task.toml files" >&2
        exit 1
    fi
done < <(find "$DST" -name task.toml -print0)

patched=$(find "$DST" -name task.toml | wc -l)
echo "processed $patched task.toml files in $DST (agent network_mode -> public where set)"
