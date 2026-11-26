-- +goose Up
CREATE TABLE hub_config_head (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0)
);

INSERT INTO hub_config_head DEFAULT VALUES;

CREATE TABLE hub_config_outbox (
  revision BIGINT PRIMARY KEY,
  transaction_id XID8 NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  published_at TIMESTAMPTZ
);

CREATE INDEX hub_config_outbox_pending_revision
  ON hub_config_outbox (revision)
  WHERE published_at IS NULL;

CREATE INDEX hub_config_outbox_published_at
  ON hub_config_outbox (published_at)
  WHERE published_at IS NOT NULL;

CREATE TABLE hub_config_changes (
  revision BIGINT NOT NULL REFERENCES hub_config_outbox (revision) ON DELETE CASCADE,
  change_id BIGINT GENERATED ALWAYS AS IDENTITY,
  resource_kind TEXT NOT NULL,
  operation TEXT NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
  previous JSONB,
  current JSONB,
  PRIMARY KEY (revision, change_id)
);

-- Lock before the statement touches resource rows. Holding this lock until
-- commit serializes metadata revisions, including direct SQL writers.
-- +goose StatementBegin
CREATE FUNCTION lock_hub_config_revision() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM 1 FROM hub_config_head WHERE singleton FOR UPDATE;
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION record_hub_config_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
  xid XID8 := pg_current_xact_id();
  transaction_revision BIGINT;
  previous JSONB;
  current JSONB;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    previous := to_jsonb(OLD);
  END IF;
  IF TG_OP <> 'DELETE' THEN
    current := to_jsonb(NEW);
  END IF;
  IF TG_OP = 'UPDATE' AND (previous - 'updated_at') = (current - 'updated_at') THEN
    RETURN NULL;
  END IF;

  SELECT revision INTO transaction_revision
    FROM hub_config_outbox
    WHERE transaction_id = xid;

  IF NOT FOUND THEN
    UPDATE hub_config_head SET revision = revision + 1 WHERE singleton
      RETURNING revision INTO STRICT transaction_revision;
    INSERT INTO hub_config_outbox (revision, transaction_id)
      VALUES (transaction_revision, xid);
  END IF;

  INSERT INTO hub_config_changes (revision, resource_kind, operation, previous, current)
    VALUES (transaction_revision, TG_ARGV[0], TG_OP, previous, current);
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER hub_config_lock_zones BEFORE INSERT OR UPDATE OR DELETE ON zones
  FOR EACH STATEMENT EXECUTE FUNCTION lock_hub_config_revision();
CREATE TRIGGER hub_config_lock_providers BEFORE INSERT OR UPDATE OR DELETE ON providers
  FOR EACH STATEMENT EXECUTE FUNCTION lock_hub_config_revision();
CREATE TRIGGER hub_config_lock_trackables BEFORE INSERT OR UPDATE OR DELETE ON trackables
  FOR EACH STATEMENT EXECUTE FUNCTION lock_hub_config_revision();
CREATE TRIGGER hub_config_lock_fences BEFORE INSERT OR UPDATE OR DELETE ON fences
  FOR EACH STATEMENT EXECUTE FUNCTION lock_hub_config_revision();

CREATE TRIGGER hub_config_change_zones AFTER INSERT OR UPDATE OR DELETE ON zones
  FOR EACH ROW EXECUTE FUNCTION record_hub_config_change('zone');
CREATE TRIGGER hub_config_change_providers AFTER INSERT OR UPDATE OR DELETE ON providers
  FOR EACH ROW EXECUTE FUNCTION record_hub_config_change('location_provider');
CREATE TRIGGER hub_config_change_trackables AFTER INSERT OR UPDATE OR DELETE ON trackables
  FOR EACH ROW EXECUTE FUNCTION record_hub_config_change('trackable');
CREATE TRIGGER hub_config_change_fences AFTER INSERT OR UPDATE OR DELETE ON fences
  FOR EACH ROW EXECUTE FUNCTION record_hub_config_change('fence');

-- +goose Down
DROP TRIGGER hub_config_change_fences ON fences;
DROP TRIGGER hub_config_change_trackables ON trackables;
DROP TRIGGER hub_config_change_providers ON providers;
DROP TRIGGER hub_config_change_zones ON zones;
DROP TRIGGER hub_config_lock_fences ON fences;
DROP TRIGGER hub_config_lock_trackables ON trackables;
DROP TRIGGER hub_config_lock_providers ON providers;
DROP TRIGGER hub_config_lock_zones ON zones;
DROP FUNCTION record_hub_config_change();
DROP FUNCTION lock_hub_config_revision();
DROP TABLE hub_config_changes;
DROP TABLE hub_config_outbox;
DROP TABLE hub_config_head;
