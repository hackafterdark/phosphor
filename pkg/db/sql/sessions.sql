-- name: CreateSession :one
INSERT INTO sessions (
    id,
    parent_session_id,
    title,
    message_count,
    prompt_tokens,
    completion_tokens,
    cost,
    summary_message_id,
    current_tokens,
    updated_at,
    created_at,
    is_stateless,
    service
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    null,
    0,
    strftime('%s', 'now'),
    strftime('%s', 'now'),
    0,
    ''
) RETURNING *;

-- name: GetSessionByID :one
SELECT *
FROM sessions
WHERE id = ? LIMIT 1;

-- name: GetLastSession :one
SELECT *
FROM sessions
ORDER BY updated_at DESC
LIMIT 1;

-- name: ListSessions :many
SELECT *
FROM sessions
WHERE parent_session_id is NULL
ORDER BY updated_at DESC;

-- name: UpdateSession :one
UPDATE sessions
SET
    title = ?,
    prompt_tokens = ?,
    completion_tokens = ?,
    summary_message_id = ?,
    cost = ?,
    todos = ?,
    current_tokens = ?
WHERE id = ?
RETURNING *;

-- name: UpdateSessionTitleAndUsage :exec
UPDATE sessions
SET
    title = ?,
    prompt_tokens = prompt_tokens + ?,
    completion_tokens = completion_tokens + ?,
    cost = cost + ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: RenameSession :exec
UPDATE sessions
SET
    title = ?
WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: UpdateStatelessSession :exec
UPDATE sessions
SET
    is_stateless = ?,
    service = ?
WHERE id = ?;

-- name: ListStatelessSessions :many
SELECT id, parent_session_id, title, message_count, prompt_tokens, completion_tokens,
       cost, updated_at, created_at, summary_message_id, todos, current_tokens,
       is_stateless, service
FROM sessions
WHERE is_stateless = 1
  AND (?1 = '' OR service = ?1)
ORDER BY created_at DESC;
