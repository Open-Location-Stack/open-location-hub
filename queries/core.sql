-- name: CreateZone :one
INSERT INTO zones (id, type, foreign_id, payload)
VALUES ($1, $2, $3, $4)
RETURNING id, type, foreign_id, payload, created_at, updated_at;

-- name: GetZone :one
SELECT id, type, foreign_id, payload, created_at, updated_at
FROM zones
WHERE id = $1;

-- name: ListZones :many
SELECT id, type, foreign_id, payload, created_at, updated_at
FROM zones
ORDER BY created_at DESC;

-- name: UpdateZone :one
UPDATE zones
SET type = $2,
    foreign_id = $3,
    payload = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING id, type, foreign_id, payload, created_at, updated_at;

-- name: DeleteZone :execrows
DELETE FROM zones
WHERE id = $1;

-- name: CreateProvider :one
INSERT INTO providers (id, type, name, payload)
VALUES ($1, $2, $3, $4)
RETURNING id, type, name, payload, created_at, updated_at;

-- name: GetProvider :one
SELECT id, type, name, payload, created_at, updated_at
FROM providers
WHERE id = $1;

-- name: ListProviders :many
SELECT id, type, name, payload, created_at, updated_at
FROM providers
ORDER BY created_at DESC;

-- name: UpdateProvider :one
UPDATE providers
SET type = $2,
    name = $3,
    payload = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING id, type, name, payload, created_at, updated_at;

-- name: DeleteProvider :execrows
DELETE FROM providers
WHERE id = $1;

-- name: CreateTrackable :one
INSERT INTO trackables (id, type, name, payload)
VALUES ($1, $2, $3, $4)
RETURNING id, type, name, payload, created_at, updated_at;

-- name: GetTrackable :one
SELECT id, type, name, payload, created_at, updated_at
FROM trackables
WHERE id = $1;

-- name: ListTrackables :many
SELECT id, type, name, payload, created_at, updated_at
FROM trackables
ORDER BY created_at DESC;

-- name: UpdateTrackable :one
UPDATE trackables
SET type = $2,
    name = $3,
    payload = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING id, type, name, payload, created_at, updated_at;

-- name: DeleteTrackable :execrows
DELETE FROM trackables
WHERE id = $1;

-- name: CreateFence :one
INSERT INTO fences (id, name, foreign_id, payload)
VALUES ($1, $2, $3, $4)
RETURNING id, name, foreign_id, payload, created_at, updated_at;

-- name: GetFence :one
SELECT id, name, foreign_id, payload, created_at, updated_at
FROM fences
WHERE id = $1;

-- name: ListFences :many
SELECT id, name, foreign_id, payload, created_at, updated_at
FROM fences
ORDER BY created_at DESC;

-- name: UpdateFence :one
UPDATE fences
SET name = $2,
    foreign_id = $3,
    payload = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING id, name, foreign_id, payload, created_at, updated_at;

-- name: DeleteFence :execrows
DELETE FROM fences
WHERE id = $1;
