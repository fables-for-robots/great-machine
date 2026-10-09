-- name: LastGenerationNumber :one
-- The newest generation of a service. No row: it has none.
SELECT number FROM generations
WHERE service = sqlc.arg(service)
ORDER BY number DESC
LIMIT 1;

-- name: InsertGeneration :exec
INSERT INTO generations (service, number, binary, args, closure, created_at, created_by)
VALUES (
    sqlc.arg(service), sqlc.arg(number), sqlc.arg(binary), sqlc.arg(args),
    sqlc.arg(closure), sqlc.arg(created_at), sqlc.arg(created_by)
);

-- name: GenerationByNumber :one
SELECT * FROM generations
WHERE service = sqlc.arg(service) AND number = sqlc.arg(number);
