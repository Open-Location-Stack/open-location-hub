-- name: ListZones :many
SELECT id, type, foreign_id, payload, created_at, updated_at
FROM zones
ORDER BY created_at DESC;

-- name: ListProviders :many
SELECT id, type, name, payload, created_at, updated_at
FROM providers
ORDER BY created_at DESC;
