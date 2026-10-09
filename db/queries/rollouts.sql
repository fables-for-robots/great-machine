-- name: InsertRollout :one
INSERT INTO rollouts (service, generation, parallel, state, reason, created_at, created_by, finished_at)
VALUES (
    sqlc.arg(service), sqlc.arg(generation), sqlc.arg(parallel), 'preparing',
    '', sqlc.arg(created_at), sqlc.arg(created_by), 0
)
RETURNING *;

-- name: SupersedeUnderWay :execrows
-- Ends the rollout of a service that is under way, if there is one.
UPDATE rollouts
SET state = 'superseded', finished_at = sqlc.arg(finished_at)
WHERE service = sqlc.arg(service) AND state IN ('preparing', 'activating');

-- name: RolloutByID :one
SELECT * FROM rollouts WHERE id = sqlc.arg(id);

-- name: LastRollout :one
SELECT * FROM rollouts
WHERE service = sqlc.arg(service)
ORDER BY id DESC
LIMIT 1;

-- name: ListRollouts :many
-- Newest first.
SELECT * FROM rollouts
WHERE service = sqlc.arg(service)
ORDER BY id DESC
LIMIT sqlc.arg(max);

-- name: SetRolloutState :execrows
-- Only a rollout that is under way is moved: the count is 0 for one that
-- was superseded, or has ended, in the meantime.
UPDATE rollouts
SET state = sqlc.arg(state), reason = sqlc.arg(reason), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id) AND state IN ('preparing', 'activating');
