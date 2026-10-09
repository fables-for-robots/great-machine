-- name: InsertService :exec
-- Makes the row of a service that has none.
INSERT INTO services (name, stable, created_at)
VALUES (sqlc.arg(name), 0, sqlc.arg(created_at))
ON CONFLICT (name) DO NOTHING;

-- name: ServiceByName :one
SELECT * FROM services WHERE name = sqlc.arg(name);

-- name: ListServices :many
SELECT * FROM services ORDER BY name;

-- name: SetStable :exec
UPDATE services SET stable = sqlc.arg(stable) WHERE name = sqlc.arg(name);
