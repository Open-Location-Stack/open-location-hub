package integration

import (
	"io"
	"testing"
)

func TestConfigurationOutboxGroupsAndRollsBackDatabaseChanges(t *testing.T) {
	suite := requireIntegrationSuite(t)
	statement := `
BEGIN;
DO $$
DECLARE
  initial_revision BIGINT;
  actual_revision BIGINT;
  kinds TEXT[];
BEGIN
  SELECT revision INTO initial_revision FROM hub_config_head WHERE singleton FOR UPDATE;
  BEGIN
    INSERT INTO providers (id, type) VALUES ('outbox-rollback-fixture', 'gps');
    RAISE EXCEPTION USING ERRCODE = 'ZZ001';
  EXCEPTION WHEN SQLSTATE 'ZZ001' THEN
    NULL;
  END;
  IF (SELECT revision FROM hub_config_head) <> initial_revision THEN
    RAISE EXCEPTION 'the rolled-back mutation advanced the revision';
  END IF;

  INSERT INTO providers (id, type) VALUES ('outbox-commit-fixture', 'gps');
  INSERT INTO zones (id, type) VALUES ('a451d318-09ad-4417-aa90-1ad576cea90e', 'local');
  UPDATE providers SET updated_at = clock_timestamp() WHERE id = 'outbox-commit-fixture';
  SELECT revision INTO actual_revision FROM hub_config_head;
  IF actual_revision <> initial_revision + 1 THEN
    RAISE EXCEPTION 'one transaction produced unexpected revision %', actual_revision;
  END IF;
  SELECT array_agg(resource_kind || ':' || operation ORDER BY change_id) INTO kinds
    FROM hub_config_changes WHERE revision = actual_revision;
  IF kinds IS DISTINCT FROM ARRAY['location_provider:INSERT', 'zone:INSERT'] THEN
    RAISE EXCEPTION 'unexpected transaction membership %', kinds;
  END IF;
END;
$$;
ROLLBACK;`
	exit, output, err := suite.postgres.Exec(suite.ctx, []string{"psql", "-U", "postgres", "-d", "openrtls", "-v", "ON_ERROR_STOP=1", "-c", statement})
	if err != nil {
		t.Fatal(err)
	}
	if exit != 0 {
		data, _ := io.ReadAll(output)
		t.Fatalf("verify transactional outbox: %s", data)
	}
}
