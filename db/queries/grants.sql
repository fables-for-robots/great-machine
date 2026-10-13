-- name: ListGrants :many
-- By role, then by service, then by ID: the order they are listed in.
SELECT * FROM grants ORDER BY role, service, id;

-- name: PutGrant :exec
-- A grant that is there already is there with the new note, and is as
-- old as it was.
INSERT INTO grants (id, role, service, note, created_at, created_by)
VALUES (sqlc.arg(id), sqlc.arg(role), sqlc.arg(service), sqlc.arg(note), sqlc.arg(created_at), sqlc.arg(created_by))
ON CONFLICT (id, role, service) DO UPDATE SET note = excluded.note;

-- name: DeleteGrant :execrows
DELETE FROM grants WHERE id = sqlc.arg(id) AND role = sqlc.arg(role) AND service = sqlc.arg(service);
