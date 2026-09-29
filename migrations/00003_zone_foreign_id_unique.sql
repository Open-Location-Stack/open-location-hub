-- +goose Up
-- OMLOX section 7.7 requires an unambiguous foreign source-to-zone mapping.
CREATE UNIQUE INDEX zones_foreign_id_unique ON zones (foreign_id)
WHERE foreign_id IS NOT NULL AND foreign_id <> '';

-- +goose Down
DROP INDEX zones_foreign_id_unique;
