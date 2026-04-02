package integration

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestObservabilityExportsOTLPLogsMetricsAndTraces(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	ctx := context.Background()
	network, err := tcnetwork.New(ctx)
	if err != nil {
		t.Skipf("docker network unavailable: %v", err)
	}
	t.Cleanup(func() { _ = network.Remove(ctx) })

	sinkReq := testcontainers.ContainerRequest{
		Image:        "python:3.12-alpine",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"otlp-sink"},
		},
		Cmd: []string{
			"python3", "-u", "-c",
			`from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('content-length', '0'))
        if length:
            self.rfile.read(length)
        print(self.path, flush=True)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{}')
    def log_message(self, fmt, *args):
        pass
HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()`,
		},
		WaitingFor: wait.ForListeningPort("8080/tcp").WithStartupTimeout(30 * time.Second),
	}
	sink, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: sinkReq,
		Started:          true,
	})
	if err != nil {
		t.Skipf("docker/otlp-sink unavailable: %v", err)
	}
	t.Cleanup(func() { _ = sink.Terminate(ctx) })

	pg, err := postgres.Run(ctx, "postgres:17",
		postgres.WithDatabase("openrtls"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		tcnetwork.WithNetwork([]string{"postgres"}, network),
	)
	if err != nil {
		t.Skipf("docker/postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	vk, err := redis.Run(ctx, "valkey/valkey:8-alpine", tcnetwork.WithNetwork([]string{"valkey"}, network))
	if err != nil {
		t.Skipf("docker/valkey unavailable: %v", err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })

	mqReq := testcontainers.ContainerRequest{
		Image:        "eclipse-mosquitto:2.0",
		ExposedPorts: []string{"1883/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"mosquitto"},
		},
		WaitingFor: wait.ForListeningPort("1883/tcp").WithStartupTimeout(30 * time.Second),
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      repoPath(t, "tools/mqtt/mosquitto.conf"),
			ContainerFilePath: "/mosquitto/config/mosquitto.conf",
			FileMode:          0o644,
		}},
	}
	mq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: mqReq,
		Started:          true,
	})
	if err != nil {
		t.Skipf("docker/mosquitto unavailable: %v", err)
	}
	t.Cleanup(func() { _ = mq.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn failed: %v", err)
	}
	runMigrations(t, ctx, dsn)

	appReq := testcontainers.ContainerRequest{
		Image:        sharedHubImage(t),
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"hub"},
		},
		Env: map[string]string{
			"HTTP_LISTEN_ADDR":                    ":8080",
			"POSTGRES_URL":                        "postgres://postgres:postgres@postgres:5432/openrtls?sslmode=disable",
			"VALKEY_URL":                          "redis://valkey:6379/0",
			"MQTT_BROKER_URL":                     "tcp://mosquitto:1883",
			"AUTH_ENABLED":                        "false",
			"AUTH_MODE":                           "none",
			"OTEL_ENABLED":                        "true",
			"OTEL_METRICS_ENABLED":                "true",
			"OTEL_TRACES_ENABLED":                 "true",
			"OTEL_LOGS_ENABLED":                   "true",
			"OTEL_EXPORTER_OTLP_INSECURE":         "true",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "http://otlp-sink:8080/v1/traces",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://otlp-sink:8080/v1/metrics",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    "http://otlp-sink:8080/v1/logs",
			"OTEL_METRIC_EXPORT_INTERVAL":         "2s",
			"OTEL_METRIC_EXPORT_TIMEOUT":          "2s",
			"OTEL_EXPORTER_OTLP_TIMEOUT":          "2s",
			"OTEL_SERVICE_NAME":                   "open-rtls-hub-integration",
			"OTEL_DEPLOYMENT_ENVIRONMENT":         "integration-test",
		},
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort("8080/tcp").
			WithStartupTimeout(90 * time.Second),
	}
	app, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: appReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("docker/app unavailable: %v", err)
	}
	t.Cleanup(func() { _ = app.Terminate(ctx) })

	appBaseURL := mappedHTTPURL(t, ctx, app, "8080/tcp")

	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", "", georeferencedZonePayload(0.5, 0.5, false))
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)

	locationResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", "", []map[string]any{{
		"crs":           "local",
		"position":      pointPayload(5, 7),
		"provider_id":   "provider-observability",
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatusAndClose(t, locationResp, http.StatusAccepted)

	if err := waitForSinkPaths(ctx, sink, 30*time.Second, "/v1/traces", "/v1/metrics", "/v1/logs"); err != nil {
		t.Fatal(err)
	}
}

func waitForSinkPaths(ctx context.Context, container testcontainers.Container, timeout time.Duration, want ...string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		logs, err := container.Logs(ctx)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(logs)
		_ = logs.Close()
		if readErr != nil {
			return readErr
		}
		found := map[string]bool{}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			for _, path := range want {
				if line == path {
					found[path] = true
				}
			}
		}
		all := true
		for _, path := range want {
			if !found[path] {
				all = false
				break
			}
		}
		if all {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timed out waiting for OTLP sink paths %v", want)
}
