-- +goose Up
CREATE TABLE hub_deployment_binding (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  binding_id TEXT NOT NULL CHECK (binding_id <> ''),
  database_mode TEXT NOT NULL CHECK (database_mode IN ('external', 'managed')),
  hub_id UUID NOT NULL
);

-- The application writes metadata with a restricted database role. The trigger
-- owns the outbox write so that role does not need access to change history.
ALTER FUNCTION public.lock_hub_config_revision() SECURITY DEFINER;
ALTER FUNCTION public.lock_hub_config_revision() SET search_path = pg_catalog, public, pg_temp;
REVOKE EXECUTE ON FUNCTION public.lock_hub_config_revision() FROM PUBLIC;
ALTER FUNCTION public.record_hub_config_change() SECURITY DEFINER;
ALTER FUNCTION public.record_hub_config_change() SET search_path = pg_catalog, public, pg_temp;
REVOKE EXECUTE ON FUNCTION public.record_hub_config_change() FROM PUBLIC;

-- +goose Down
GRANT EXECUTE ON FUNCTION public.record_hub_config_change() TO PUBLIC;
ALTER FUNCTION public.record_hub_config_change() RESET search_path;
ALTER FUNCTION public.record_hub_config_change() SECURITY INVOKER;
GRANT EXECUTE ON FUNCTION public.lock_hub_config_revision() TO PUBLIC;
ALTER FUNCTION public.lock_hub_config_revision() RESET search_path;
ALTER FUNCTION public.lock_hub_config_revision() SECURITY INVOKER;
DROP TABLE hub_deployment_binding;
