-- name: HubConfigRevision :one
SELECT revision FROM hub_config_head WHERE singleton;

-- name: LockNextHubConfigRevision :one
SELECT revision, created_at
FROM hub_config_outbox
WHERE published_at IS NULL
ORDER BY revision
LIMIT 1
FOR UPDATE;

-- name: ListHubConfigChanges :many
SELECT resource_kind, operation, previous, current
FROM hub_config_changes
WHERE revision = $1
ORDER BY change_id;

-- name: MarkHubConfigPublished :exec
UPDATE hub_config_outbox
SET published_at = clock_timestamp()
WHERE revision = $1 AND published_at IS NULL;

-- name: PruneHubConfigRevisions :execrows
DELETE FROM hub_config_outbox
WHERE revision IN (
  SELECT sent.revision
  FROM hub_config_outbox AS sent
  WHERE sent.published_at < sqlc.arg(published_before)
  ORDER BY sent.published_at
  LIMIT sqlc.arg(batch_size)
  FOR UPDATE SKIP LOCKED
);
