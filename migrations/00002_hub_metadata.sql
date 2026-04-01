-- +goose Up
CREATE TABLE IF NOT EXISTS hub_metadata (
  singleton_id SMALLINT PRIMARY KEY CHECK (singleton_id = 1),
  hub_id UUID NOT NULL,
  label TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS hub_metadata;
