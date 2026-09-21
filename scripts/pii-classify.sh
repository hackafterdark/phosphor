#!/usr/bin/env bash

set -euo pipefail

: "${PII_CLASSIFY_CMD:=}"
: "${PII_CONFIDENCE_THRESHOLD:=0.8}"
: "${PII_CLASSIFY_FILES:=0}"

if ! command -v jq >/dev/null 2>&1; then
  exit 0
fi

if [[ -z "$PII_CLASSIFY_CMD" ]]; then
  exit 0
fi

payload="$(cat)"

text="$(jq -r '
  [
    .tool_name // "",
    .tool_input.command // "",
    .tool_input.description // "",
    .tool_input.content // "",
    .tool_input.old_string // "",
    .tool_input.new_string // "",
    (.tool_input.edits? // [] | map(.old_string // "", .new_string // "") | join("\n")),
    .tool_input.pattern // "",
    .tool_input.path // "",
    .tool_input.file_path // ""
  ]
  | map(select(. != "" and . != null))
  | unique
  | join("\n")
' <<<"$payload")" || exit 0

if [[ "$PII_CLASSIFY_FILES" == "1" ]]; then
  target_path="$(jq -r '.tool_input.file_path // empty' <<<"$payload")" || exit 0
  if [[ -n "$target_path" ]]; then
    case "$target_path" in
      /*) ;;
      [A-Za-z]:/*) ;;
      *) target_path="$PWD/$target_path" ;;
    esac

    project_dir="${PHOSPHOR_PROJECT_DIR:-$PWD}"
    project_dir="${project_dir%/}"

    if [[ "$target_path" == "$project_dir"/* && -f "$target_path" ]]; then
      text+=$'\n'
      text+="$(cat "$target_path")"
    fi
  fi
fi

if [[ -z "$text" ]]; then
  exit 0
fi

score_json="$(printf '%s' "$text" | eval "$PII_CLASSIFY_CMD" 2>/dev/null)" || exit 0

if [[ -z "$score_json" ]]; then
  exit 0
fi

is_high="$(jq --argjson score "$score_json" --arg threshold "$PII_CONFIDENCE_THRESHOLD" -n '
  ($score.decision? // null) as $decision
  | (($score.confidence // $score.score // 0) | tonumber? // 0) as $confidence
  | ($threshold | tonumber? // 0.8) as $threshold
  | if $decision == "pii" then
      true
    elif $decision == null and $confidence >= $threshold then
      true
    else
      false
    end
')" || exit 0

if [[ "$is_high" == "true" ]]; then
  printf '%s\n' '{"decision":"deny","reason":"PII detected by pii-classify hook"}'
fi

exit 0
