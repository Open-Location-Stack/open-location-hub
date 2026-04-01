-- name: GetHubMetadata :one
SELECT singleton_id, hub_id, label, created_at, updated_at
FROM hub_metadata
WHERE singleton_id = 1;

-- name: CreateHubMetadata :one
INSERT INTO hub_metadata (singleton_id, hub_id, label)
VALUES (1, $1, $2)
RETURNING singleton_id, hub_id, label, created_at, updated_at;

-- name: UpdateHubMetadata :one
UPDATE hub_metadata
SET hub_id = $1,
    label = $2,
    updated_at = NOW()
WHERE singleton_id = 1
RETURNING singleton_id, hub_id, label, created_at, updated_at;
