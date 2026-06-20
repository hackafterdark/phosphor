-- name: GetUsageByDayRange :many
SELECT
    date(tu.created_at, 'unixepoch') as day,
    SUM(tu.prompt_tokens) as prompt_tokens,
    SUM(tu.completion_tokens) as completion_tokens,
    SUM(tu.cost) as cost,
    COUNT(DISTINCT COALESCE(s.parent_session_id, tu.session_id)) as session_count
FROM token_usage tu
LEFT JOIN sessions s ON tu.session_id = s.id
WHERE tu.created_at >= strftime('%s', 'now', ?1)
GROUP BY date(tu.created_at, 'unixepoch')
ORDER BY day ASC;

-- name: GetUsageByDay :many
SELECT
    date(tu.created_at, 'unixepoch') as day,
    SUM(tu.prompt_tokens) as prompt_tokens,
    SUM(tu.completion_tokens) as completion_tokens,
    SUM(tu.cost) as cost,
    COUNT(DISTINCT COALESCE(s.parent_session_id, tu.session_id)) as session_count
FROM token_usage tu
LEFT JOIN sessions s ON tu.session_id = s.id
GROUP BY date(tu.created_at, 'unixepoch')
ORDER BY day DESC;

-- name: GetUsageByModel :many
SELECT
    COALESCE(model, 'unknown') as model,
    COALESCE(provider, 'unknown') as provider,
    COUNT(*) as message_count
FROM messages
WHERE role = 'assistant'
GROUP BY model, provider
ORDER BY message_count DESC;

-- name: GetUsageByHour :many
SELECT
    CAST(strftime('%H', created_at, 'unixepoch') AS INTEGER) as hour,
    COUNT(*) as session_count
FROM sessions
WHERE parent_session_id IS NULL
GROUP BY hour
ORDER BY hour;

-- name: GetUsageByDayOfWeek :many
SELECT
    CAST(strftime('%w', tu.created_at, 'unixepoch') AS INTEGER) as day_of_week,
    COUNT(DISTINCT COALESCE(s.parent_session_id, tu.session_id)) as session_count,
    SUM(tu.prompt_tokens) as prompt_tokens,
    SUM(tu.completion_tokens) as completion_tokens
FROM token_usage tu
LEFT JOIN sessions s ON tu.session_id = s.id
GROUP BY day_of_week
ORDER BY day_of_week;

-- name: GetTotalStats :one
SELECT
    (SELECT COUNT(*) FROM sessions WHERE parent_session_id IS NULL) as total_sessions,
    COALESCE((SELECT SUM(prompt_tokens) FROM token_usage), 0) as total_prompt_tokens,
    COALESCE((SELECT SUM(completion_tokens) FROM token_usage), 0) as total_completion_tokens,
    COALESCE((SELECT SUM(cost) FROM token_usage), 0) as total_cost,
    COALESCE((SELECT SUM(message_count) FROM sessions WHERE parent_session_id IS NULL), 0) as total_messages,
    COALESCE((SELECT AVG(session_sum) FROM (SELECT SUM(prompt_tokens + completion_tokens) as session_sum FROM token_usage GROUP BY session_id)), 0) as avg_tokens_per_session,
    COALESCE((SELECT AVG(message_count) FROM sessions WHERE parent_session_id IS NULL), 0) as avg_messages_per_session;

-- name: GetRecentActivity :many
SELECT
    date(tu.created_at, 'unixepoch') as day,
    COUNT(DISTINCT COALESCE(s.parent_session_id, tu.session_id)) as session_count,
    SUM(tu.prompt_tokens + tu.completion_tokens) as total_tokens,
    SUM(tu.cost) as cost
FROM token_usage tu
LEFT JOIN sessions s ON tu.session_id = s.id
WHERE tu.created_at >= strftime('%s', 'now', '-30 days')
GROUP BY date(tu.created_at, 'unixepoch')
ORDER BY day ASC;

-- name: GetAverageResponseTime :one
SELECT
    CAST(COALESCE(AVG(finished_at - created_at), 0) AS INTEGER) as avg_response_seconds
FROM messages
WHERE role = 'assistant'
  AND finished_at IS NOT NULL
  AND finished_at > created_at;

-- name: GetToolUsage :many
SELECT
    json_extract(value, '$.data.name') as tool_name,
    COUNT(*) as call_count
FROM messages, json_each(parts)
WHERE json_extract(value, '$.type') = 'tool_call'
  AND json_extract(value, '$.data.name') IS NOT NULL
GROUP BY tool_name
ORDER BY call_count DESC;

-- name: GetHourDayHeatmap :many
SELECT
    CAST(strftime('%w', created_at, 'unixepoch') AS INTEGER) as day_of_week,
    CAST(strftime('%H', created_at, 'unixepoch') AS INTEGER) as hour,
    COUNT(*) as session_count
FROM sessions
WHERE parent_session_id IS NULL
GROUP BY day_of_week, hour
ORDER BY day_of_week, hour;

-- name: RecordTokenUsage :exec
INSERT INTO token_usage (
    id,
    session_id,
    model,
    provider,
    prompt_tokens,
    completion_tokens,
    cost,
    created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now')
);
