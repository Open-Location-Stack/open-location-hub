package integration

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestIntegrationHarness(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:17", postgres.WithDatabase("openrtls"), postgres.WithUsername("postgres"), postgres.WithPassword("postgres"))
	if err != nil {
		t.Skipf("docker/postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	vk, err := redis.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Skipf("docker/valkey unavailable: %v", err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })

	mqReq := testcontainers.ContainerRequest{
		Image:        "eclipse-mosquitto:2.0",
		ExposedPorts: []string{"1883/tcp"},
		WaitingFor:   wait.ForListeningPort("1883/tcp").WithStartupTimeout(30 * time.Second),
	}
	mq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: mqReq, Started: true})
	if err != nil {
		t.Skipf("docker/mosquitto unavailable: %v", err)
	}
	t.Cleanup(func() { _ = mq.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn failed: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql open failed: %v", err)
	}
	defer db.Close()

	_, thisFile, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations"))
	if err := goose.Up(db, migrationsDir); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	var cnt int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_name='zones'").Scan(&cnt); err != nil {
		t.Fatalf("table check query failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("zones table missing")
	}

	host, err := mq.Host(ctx)
	if err != nil {
		t.Fatalf("mosquitto host failed: %v", err)
	}
	port, err := mq.MappedPort(ctx, "1883")
	if err != nil {
		t.Fatalf("mosquitto port failed: %v", err)
	}
	if host == "" || port.Int() == 0 {
		t.Fatalf("invalid mqtt endpoint %s:%s", host, port.Port())
	}
	_ = fmt.Sprintf("mqtt endpoint %s:%s", host, port.Port())
}
