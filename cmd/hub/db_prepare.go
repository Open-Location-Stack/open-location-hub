package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/formation-res/open-location-hub/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

type databaseBinding struct {
	id, mode, name string
	hubID          uuid.UUID
	insecureLocal  bool
	minVersion     int
	requiredMajor  int
}

func databaseBindingFromEnv() (databaseBinding, error) {
	b := databaseBinding{
		id: os.Getenv("HUB_DB_BINDING_ID"), mode: os.Getenv("HUB_DB_MODE"),
		name: os.Getenv("HUB_DB_NAME"), insecureLocal: os.Getenv("HUB_DB_INSECURE_LOCAL") == "true",
	}
	id, err := uuid.Parse(os.Getenv("HUB_ID"))
	if err != nil || id == uuid.Nil || b.id == "" || b.name == "" || (b.mode != "external" && b.mode != "managed") {
		return b, errors.New("database preparation requires HUB_ID, HUB_DB_NAME, HUB_DB_BINDING_ID and a valid HUB_DB_MODE")
	}
	b.hubID = id
	b.minVersion, err = strconv.Atoi(workerEnv("HUB_DB_MIN_VERSION_NUM", "170000"))
	if err != nil || b.minVersion < 170000 {
		return b, errors.New("HUB_DB_MIN_VERSION_NUM must name PostgreSQL 17 or later")
	}
	if b.mode == "managed" {
		b.requiredMajor, err = strconv.Atoi(os.Getenv("HUB_DB_REQUIRED_MAJOR"))
		if err != nil || b.requiredMajor != b.minVersion/10000 {
			return b, errors.New("managed PostgreSQL requires a qualified major and minimum patch")
		}
	}
	return b, nil
}

type databasePeer struct {
	user      string
	address   string
	startedAt time.Time
}

func (b databaseBinding) connect(ctx context.Context, dsn, password, role string) (*pgx.Conn, databasePeer, error) {
	var peer databasePeer
	if dsn == "" {
		return nil, peer, fmt.Errorf("missing %s database connection", role)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, peer, fmt.Errorf("invalid %s database connection", role)
	}
	if searchPath := cfg.RuntimeParams["search_path"]; searchPath != "" && searchPath != "public" && searchPath != "pg_catalog,public" {
		return nil, peer, fmt.Errorf("%s database connection requires the public Hub schema", role)
	}
	if password != "" {
		cfg.Password = password
	}
	verifyTLS := cfg.TLSConfig != nil && !cfg.TLSConfig.InsecureSkipVerify && cfg.TLSConfig.ServerName != ""
	for _, fallback := range cfg.Fallbacks {
		verifyTLS = verifyTLS && fallback.TLSConfig != nil && !fallback.TLSConfig.InsecureSkipVerify && fallback.TLSConfig.ServerName != ""
	}
	if !b.insecureLocal && !verifyTLS {
		return nil, peer, fmt.Errorf("%s database connection requires PostgreSQL TLS hostname verification", role)
	}
	cfg.RuntimeParams["search_path"] = "pg_catalog,public"
	cfg.ConnectTimeout = 10 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, peer, fmt.Errorf("%s PostgreSQL connection failed: %w", role, err)
	}
	var name string
	var version int
	var writable, privileged, canCreate bool
	err = conn.QueryRow(ctx, `
SELECT current_user, current_database(), current_setting('server_version_num')::integer,
       NOT pg_is_in_recovery() AND current_setting('transaction_read_only') = 'off',
       coalesce(inet_server_addr()::text, 'local'), pg_postmaster_start_time(),
       r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication OR r.rolbypassrls,
       has_schema_privilege(current_user, 'public', 'CREATE')
FROM pg_roles r WHERE r.rolname = current_user`).Scan(
		&peer.user, &name, &version, &writable, &peer.address, &peer.startedAt, &privileged, &canCreate)
	if err != nil || name != b.name || !writable || version < b.minVersion ||
		(b.requiredMajor != 0 && version/10000 != b.requiredMajor) ||
		privileged || (role == "migrator") != canCreate {
		_ = conn.Close(context.Background())
		if err != nil {
			return nil, peer, fmt.Errorf("inspect %s PostgreSQL permissions and identity: %w", role, err)
		}
		return nil, peer, fmt.Errorf("%s PostgreSQL role, database, writable primary or version failed preflight", role)
	}
	return conn, peer, nil
}

func (b databaseBinding) check(ctx context.Context, conn *pgx.Conn, allowUninitialized bool) error {
	var relationExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.hub_deployment_binding') IS NOT NULL").Scan(&relationExists); err != nil {
		return err
	}
	if !relationExists && allowUninitialized {
		return nil
	}
	if !relationExists {
		return errors.New("PostgreSQL has no Hub deployment binding")
	}
	var id, mode string
	var hubID uuid.UUID
	err := conn.QueryRow(ctx, "SELECT binding_id, database_mode, hub_id FROM public.hub_deployment_binding WHERE singleton").Scan(&id, &mode, &hubID)
	if errors.Is(err, pgx.ErrNoRows) && allowUninitialized {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read PostgreSQL deployment binding: %w", err)
	}
	if id != b.id || mode != b.mode || hubID != b.hubID {
		return errors.New("PostgreSQL is already bound to another deployment")
	}
	return nil
}

func (b databaseBinding) verifyHubIdentity(ctx context.Context, conn *pgx.Conn) error {
	var relationExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.hub_metadata') IS NOT NULL").Scan(&relationExists); err != nil || !relationExists {
		return err
	}
	var hubID uuid.UUID
	err := conn.QueryRow(ctx, "SELECT hub_id FROM public.hub_metadata WHERE singleton_id = 1").Scan(&hubID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if hubID != b.hubID {
		return errors.New("persisted Hub ID differs from the deployment settings")
	}
	return nil
}

func runDatabaseCheck(ctx context.Context) error {
	b, err := databaseBindingFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, _, err := b.connect(ctx, os.Getenv("POSTGRES_URL"), "", "runtime")
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	return b.check(ctx, conn, false)
}

func runDatabasePrepare(ctx context.Context) error {
	b, err := databaseBindingFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	migrator, owner, err := b.connect(ctx, os.Getenv("HUB_DB_MIGRATION_URL"), os.Getenv("HUB_DB_MIGRATION_PASSWORD"), "migrator")
	if err != nil {
		return err
	}
	defer migrator.Close(context.WithoutCancel(ctx))
	if _, err := migrator.Exec(ctx, "SELECT pg_advisory_lock($1::bigint)", lock.DefaultLockID); err != nil {
		return fmt.Errorf("lock PostgreSQL migrations: %w", err)
	}
	// The session lock is released when the dedicated connection closes.
	if err := b.check(ctx, migrator, true); err != nil {
		return err
	}
	if err := b.verifyHubIdentity(ctx, migrator); err != nil {
		return err
	}

	runtime, runtimePeer, err := b.connect(ctx, os.Getenv("HUB_DB_RUNTIME_URL"), os.Getenv("HUB_DB_RUNTIME_PASSWORD"), "runtime")
	if err != nil {
		return err
	}
	defer runtime.Close(context.WithoutCancel(ctx))
	if runtimePeer.user == owner.user || runtimePeer.address != owner.address || !runtimePeer.startedAt.Equal(owner.startedAt) {
		return errors.New("runtime and migration credentials must be distinct roles on the same PostgreSQL primary")
	}
	var relay *pgx.Conn
	relayUser := ""
	if relayURL := os.Getenv("HUB_DB_RELAY_URL"); relayURL != "" {
		var peer databasePeer
		relay, peer, err = b.connect(ctx, relayURL, os.Getenv("HUB_DB_RELAY_PASSWORD"), "relay")
		if err != nil {
			return err
		}
		defer relay.Close(context.WithoutCancel(ctx))
		if peer.user == owner.user || peer.user == runtimePeer.user || peer.address != owner.address || !peer.startedAt.Equal(owner.startedAt) {
			return errors.New("relay credentials must be a separate role on the same PostgreSQL primary")
		}
		relayUser = peer.user
	}
	if err := migrateDatabase(ctx, migrator.Config()); err != nil {
		return err
	}
	if err := b.bindAndGrant(ctx, migrator, runtimePeer.user, relayUser); err != nil {
		return err
	}
	if err := b.check(ctx, runtime, false); err != nil {
		return err
	}
	if relay != nil {
		return b.check(ctx, relay, false)
	}
	return nil
}

func migrateDatabase(ctx context.Context, cfg *pgx.ConnConfig) error {
	// Goose uses public for its version table as the local migration CLI does.
	cfg.RuntimeParams["search_path"] = "public,pg_catalog"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		return fmt.Errorf("initialize PostgreSQL migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply PostgreSQL migrations: %w", err)
	}
	return nil
}

func (b databaseBinding) bindAndGrant(ctx context.Context, conn *pgx.Conn, runtimeUser, relayUser string) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO public.hub_deployment_binding (binding_id, database_mode, hub_id) VALUES ($1,$2,$3)
ON CONFLICT (singleton) DO NOTHING`, b.id, b.mode, b.hubID); err != nil {
			return fmt.Errorf("save PostgreSQL deployment binding: %w", err)
		}
		var actualID uuid.UUID
		if err := tx.QueryRow(ctx, `
INSERT INTO public.hub_metadata (singleton_id, hub_id, label) VALUES (1,$1,$2)
ON CONFLICT (singleton_id) DO UPDATE SET hub_id = hub_metadata.hub_id
RETURNING hub_id`, b.hubID, workerEnv("HUB_LABEL", "open-location-hub")).Scan(&actualID); err != nil {
			return fmt.Errorf("initialize Hub identity: %w", err)
		}
		if actualID != b.hubID {
			return errors.New("persisted Hub ID differs from the deployment settings")
		}

		runtime := pgx.Identifier{runtimeUser}.Sanitize()
		if _, err := tx.Exec(ctx, "GRANT SELECT, INSERT, UPDATE, DELETE ON public.zones, public.providers, public.trackables, public.fences TO "+runtime); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "GRANT SELECT ON public.hub_metadata, public.hub_deployment_binding TO "+runtime); err != nil {
			return err
		}
		if relayUser != "" {
			relay := pgx.Identifier{relayUser}.Sanitize()
			if _, err := tx.Exec(ctx, "GRANT SELECT ON public.hub_metadata, public.hub_deployment_binding, public.hub_config_head, public.hub_config_changes TO "+relay); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "GRANT SELECT, DELETE, UPDATE (published_at) ON public.hub_config_outbox TO "+relay); err != nil {
				return err
			}
		}
		return nil
	})
}
