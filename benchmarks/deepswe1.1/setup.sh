#!/usr/bin/env bash
# One-time setup for the DeepSWE v1.1 benchmark kit.
#
# Creates the Pier venv, clones the task repo, and (on request) builds the
# container-safe Phosphor binary. The v1.1 environments are prebuilt ECR
# images, so no local image builds are needed here.
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$BENCH_DIR/../.." && pwd)"
VENV="${PIER_VENV:-$REPO_ROOT/.tmp/pier}"
TASKS_DIR="${DEEPSWE_TASKS_DIR:-$REPO_ROOT/.tmp/deep-swe}"

# 1. Pier (Harbor fork used for leaderboard-grade v1.1 runs). Lives in
# .tmp/pier by default so the ~400 MB venv never sits in the repo tree.
if [ ! -x "$VENV/bin/pier" ]; then
    echo "==> Installing datacurve-pier into $VENV"
    if command -v uv >/dev/null 2>&1; then
        UV_CACHE_DIR="$BENCH_DIR/.uvcache" uv venv "$VENV" --python python3
        UV_CACHE_DIR="$BENCH_DIR/.uvcache" uv pip install -p "$VENV/bin/python" "datacurve-pier>0.3.0"
    else
        python3 -m venv "$VENV"
        "$VENV/bin/pip" install "datacurve-pier>0.3.0"
    fi
fi
echo "==> pier $($VENV/bin/pier --version) ready"

# 2. Task repo (113 v1.1 tasks in Harbor format).
if [ ! -d "$TASKS_DIR/tasks" ]; then
    echo "==> Cloning deep-swe tasks into $TASKS_DIR"
    git clone --depth 1 https://github.com/datacurve-ai/deep-swe "$TASKS_DIR"
fi
echo "==> tasks: $(ls "$TASKS_DIR/tasks" | wc -l) directories"

# 3. Container-safe Phosphor binary (glibc 2.35 baseline via bullseye).
if [ "${1:-}" = "--build-binary" ]; then
    echo "==> Building .tmp/phosphor-cgo-231 inside golang:1.23-bullseye"
    docker run --rm -v "$REPO_ROOT:/src" \
        -e GOTOOLCHAIN=auto -e CGO_ENABLED=1 \
        -e GOMODCACHE=/src/.tmp/gomodcache -e GOPATH=/src/.tmp/gopath \
        golang:1.23-bullseye \
        bash -c 'cd /src && go build -buildvcs=false -o .tmp/phosphor-cgo-231 .'
else
    if [ ! -f "$REPO_ROOT/.tmp/phosphor-cgo-231" ]; then
        echo "!! no container binary found; run: $0 --build-binary" >&2
    fi
fi

echo "==> Setup complete. Smoke test:"
echo "    $BENCH_DIR/run.sh --task <task-id>"

# 4. Network-open task copy for local Docker runs (see prepare_tasks.sh).
"$BENCH_DIR/prepare_tasks.sh"
