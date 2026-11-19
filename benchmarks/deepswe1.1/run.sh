#!/usr/bin/env bash
# Run DeepSWE v1.1 tasks with Phosphor via Pier.
#
# Usage:
#   benchmarks/deepswe1.1/run.sh --task <task-id> [--config arms/<arm>.json]
#   benchmarks/deepswe1.1/run.sh                      # full 113-task run
#
# Environment overrides:
#   DEEPSWE_TASKS_DIR   task repo checkout   (default: .tmp/deep-swe)
#   DEEPSWE_BINARY      container binary     (default: .tmp/phosphor-cgo-231)
#   DEEPSWE_CONFIG      arm config           (default: arms/deepseek-v41-flash.json)
#   DEEPSWE_CONCURRENT  parallel trials      (default: 2)
#   DEEPSWE_API_KEY_ENV env var holding the provider key (default: OLLAMA_API_KEY)
#   DEEPSWE_API_KEY     literal key value (overrides the env-var lookup)
#
# Any extra arguments are forwarded verbatim to `pier run`.
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$BENCH_DIR/../.." && pwd)"
VENV="${PIER_VENV:-$REPO_ROOT/.tmp/pier}"
TASKS_DIR="${DEEPSWE_TASKS_DIR:-$BENCH_DIR/tasks-open}"
BINARY="${DEEPSWE_BINARY:-$REPO_ROOT/.tmp/phosphor-cgo-231}"
CONFIG="${DEEPSWE_CONFIG:-$BENCH_DIR/arms/deepseek-v41-flash.json}"
# Default concurrency is 2: each trial holds its own multi-GB environment
# image on disk, and higher counts were disk-limited on past sweeps.
CONCURRENT="${DEEPSWE_CONCURRENT:-2}"

[ -x "$VENV/bin/pier" ] || { echo "pier venv missing; run $BENCH_DIR/setup.sh first" >&2; exit 1; }
[ -d "$TASKS_DIR" ] || { echo "tasks dir $TASKS_DIR missing; run: $BENCH_DIR/prepare_tasks.sh" >&2; exit 1; }
# tasks-open holds the task dirs directly; the raw clone nests them under tasks/.
case "$TASKS_DIR" in
    */tasks-open) TASK_PATH="$TASKS_DIR" ;;
    *) TASK_PATH="$TASKS_DIR/tasks" ;;
esac
[ -f "$BINARY" ] || { echo "binary $BINARY missing; run: $BENCH_DIR/setup.sh --build-binary" >&2; exit 1; }
[ -f "$CONFIG" ] || { echo "arm config $CONFIG missing" >&2; exit 1; }

# Resolve the provider credential on the host and forward it into the agent.
API_KEY_ENV="${DEEPSWE_API_KEY_ENV:-OLLAMA_API_KEY}"
API_KEY="${DEEPSWE_API_KEY:-${!API_KEY_ENV:-}}"
if [ -z "$API_KEY" ] && [ -f "$REPO_ROOT/.tmp/ollama-key.txt" ]; then
    API_KEY="$(cat "$REPO_ROOT/.tmp/ollama-key.txt")"
fi
if [ -z "$API_KEY" ]; then
    echo "no provider credential: set $API_KEY_ENV or DEEPSWE_API_KEY" >&2
    exit 1
fi

# `--task <id>` is sugar for filtering the dataset to one task name.
ARGS=()
while [ $# -gt 0 ]; do
    case "$1" in
        --task) ARGS+=(-i "$2"); shift 2 ;;
        *) ARGS+=("$1"); shift ;;
    esac
done

cd "$BENCH_DIR"
# Pier's docker-composer calls `docker compose` under the invoking user; if
# $HOME/.docker cannot be created the verifier build dies with
# "mkdir /home/<user>/.docker: permission denied". Redirect docker's config
# dir into the workspace.
export DOCKER_CONFIG="${DOCKER_CONFIG:-$REPO_ROOT/.tmp/dockerconfig}"

# Anti-cheat egress proxy: when the host exports HTTPS_PROXY / HTTP_PROXY
# (e.g. a mitmproxy enforcing a reward-seeking blocklist), forward it into
# the agent container. A host-side loopback address is unreachable from the
# container, so retarget it at the Docker host gateway; keep every other
# host:port as given. NO_PROXY is forwarded unchanged (it excludes model
# endpoints from blocking).
EXTRA_ARGS=()
for var in HTTPS_PROXY HTTP_PROXY; do
    value="${!var:-}"
    [ -n "$value" ] || continue
    case "$value" in
        *://localhost*|*://127.0.0.1*)
            value="$(printf '%s' "$value" | sed -E 's#(://)(localhost|127\.0\.0\.1)#\1host.docker.internal#')"
            ;;
    esac
    EXTRA_ARGS+=(--ae "$var=$value")
done
if [ -n "${NO_PROXY:-}" ]; then
    EXTRA_ARGS+=(--ae "NO_PROXY=${NO_PROXY}")
fi

# Pier passes no extra_hosts of its own for Docker environments, so on
# native Linux Docker Engine host.docker.internal may not resolve inside
# the agent container. Fail before the first trial instead of mid-run.
for arg in "${EXTRA_ARGS[@]+"${EXTRA_ARGS[@]}"}"; do
    case "$arg" in
        *host.docker.internal*)
            if ! docker run --rm alpine getent hosts host.docker.internal >/dev/null 2>&1; then
                echo "proxy uses host.docker.internal, but it does not resolve inside containers." >&2
                echo "On native Linux Docker (>= 20.10) pier does not add a host-gateway mapping;" >&2
                echo "run the proxy on the host's LAN IP instead, or add host-gateway to the" >&2
                echo "compose services pier generates (see README, Anti-cheat egress proxy)." >&2
                exit 1
            fi
            break
            ;;
    esac
done

exec env PYTHONPATH="$BENCH_DIR${PYTHONPATH:+:$PYTHONPATH}" \
    "$VENV/bin/pier" run \
    -p "$TASK_PATH" \
    --agent-import-path agents.phosphor:PhosphorAgent \
    --ak "binary=$BINARY" \
    --ak "config=$CONFIG" \
    --ak "skip_model_parsing=true" \
    --ae "$API_KEY_ENV=$API_KEY" \
    "${EXTRA_ARGS[@]}" \
    --n-concurrent "$CONCURRENT" \
    --jobs-dir "$BENCH_DIR/jobs" \
    "${ARGS[@]+"${ARGS[@]}"}"
