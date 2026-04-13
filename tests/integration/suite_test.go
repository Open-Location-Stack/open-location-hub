package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

type integrationSuite struct {
	ctx         context.Context
	network     *testcontainers.DockerNetwork
	postgres    testcontainers.Container
	valkey      testcontainers.Container
	mosquitto   testcontainers.Container
	dex         testcontainers.Container
	otlpSink    testcontainers.Container
	app         testcontainers.Container
	appBaseURL  string
	brokerURL   string
	adminToken  string
	readerToken string
	ownerToken  string
}

var (
	suiteMu        sync.Mutex
	sharedSuite    *integrationSuite
	sharedSuiteErr error
)

func requireIntegrationSuite(t *testing.T) *integrationSuite {
	t.Helper()

	suiteMu.Lock()
	defer suiteMu.Unlock()

	if sharedSuite != nil {
		return sharedSuite
	}
	if sharedSuiteErr != nil {
		t.Fatalf("integration suite startup failed: %v", sharedSuiteErr)
	}

	suite, err := startIntegrationSuite()
	if err != nil {
		sharedSuiteErr = err
		t.Fatalf("integration suite startup failed: %v", err)
	}
	sharedSuite = suite
	return suite
}

func shutdownIntegrationSuite() error {
	suiteMu.Lock()
	defer suiteMu.Unlock()

	if sharedSuite == nil {
		return nil
	}

	var errs []string
	for _, entry := range []struct {
		name      string
		container testcontainers.Container
	}{
		{name: "hub", container: sharedSuite.app},
		{name: "otlp-sink", container: sharedSuite.otlpSink},
		{name: "dex", container: sharedSuite.dex},
		{name: "mosquitto", container: sharedSuite.mosquitto},
		{name: "valkey", container: sharedSuite.valkey},
		{name: "postgres", container: sharedSuite.postgres},
	} {
		if entry.container == nil {
			continue
		}
		if err := entry.container.Terminate(sharedSuite.ctx); err != nil {
			errs = append(errs, fmt.Sprintf("%s terminate failed: %v", entry.name, err))
		}
	}
	if sharedSuite.network != nil {
		if err := sharedSuite.network.Remove(sharedSuite.ctx); err != nil {
			errs = append(errs, fmt.Sprintf("network remove failed: %v", err))
		}
	}
	sharedSuite = nil
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func startIntegrationSuite() (*integrationSuite, error) {
	ctx := context.Background()

	network, err := tcnetwork.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker network unavailable: %w", err)
	}

	suite := &integrationSuite{ctx: ctx, network: network}
	defer func() {
		if err != nil {
			_ = shutdownPartialSuite(suite)
		}
	}()

	suite.postgres, err = startSuitePostgres(ctx, network.Name)
	if err != nil {
		return nil, err
	}

	suite.valkey, err = redis.Run(ctx, "valkey/valkey:8-alpine", tcnetwork.WithNetwork([]string{"valkey"}, network))
	if err != nil {
		return nil, fmt.Errorf("docker/valkey unavailable: %w", err)
	}

	mqReq := testcontainers.ContainerRequest{
		Image:        "eclipse-mosquitto:2.0",
		ExposedPorts: []string{"1883/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"mosquitto"},
		},
		WaitingFor: wait.ForListeningPort("1883/tcp").WithStartupTimeout(30 * time.Second),
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(repoRoot(), "tools/mqtt/mosquitto.conf"),
			ContainerFilePath: "/mosquitto/config/mosquitto.conf",
			FileMode:          0o644,
		}},
	}
	suite.mosquitto, err = startLoggedContainer(ctx, mqReq)
	if err != nil {
		return nil, fmt.Errorf("docker/mosquitto unavailable: %w", err)
	}

	dexReq := testcontainers.ContainerRequest{
		Image:        "ghcr.io/dexidp/dex:v2.43.1",
		ExposedPorts: []string{"5556/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"dex"},
		},
		Cmd: []string{"dex", "serve", "/etc/dex/config.yaml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(repoRoot(), "tools/dex/config.yaml"),
			ContainerFilePath: "/etc/dex/config.yaml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForHTTP("/dex/.well-known/openid-configuration").
			WithPort("5556/tcp").
			WithStartupTimeout(30 * time.Second),
	}
	suite.dex, err = startLoggedContainer(ctx, dexReq)
	if err != nil {
		return nil, fmt.Errorf("docker/dex unavailable: %w", err)
	}

	sinkReq := testcontainers.ContainerRequest{
		Image:        "python:3.13-alpine",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"otlp-sink"},
		},
		Cmd: []string{"python", "-u", "-c", `from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", 0))
        body = self.rfile.read(length)
        print(self.path)
        print(body.decode("utf-8", errors="ignore"))
        self.send_response(200)
        self.end_headers()
    def log_message(self, format, *args):
        return
HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()`},
		WaitingFor: wait.ForListeningPort("8080/tcp").WithStartupTimeout(30 * time.Second),
	}
	suite.otlpSink, err = startLoggedContainer(ctx, sinkReq)
	if err != nil {
		return nil, fmt.Errorf("docker/otlp-sink unavailable: %w", err)
	}

	if err = runSuiteMigrations(ctx, suite.postgres); err != nil {
		return nil, err
	}
	if err = verifyHubMetadataTableNoTest(ctx, suite.postgres); err != nil {
		return nil, err
	}

	appReq := testcontainers.ContainerRequest{
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"hub"},
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(repoRoot(), "config/auth/permissions.yaml"),
			ContainerFilePath: "/app/config/auth/permissions.yaml",
			FileMode:          0o644,
		}},
		Env: map[string]string{
			"HTTP_LISTEN_ADDR":                    ":8080",
			"POSTGRES_URL":                        "postgres://postgres:postgres@postgres:5432/openrtls?sslmode=disable",
			"VALKEY_URL":                          "redis://valkey:6379/0",
			"MQTT_BROKER_URL":                     "tcp://mosquitto:1883",
			"AUTH_MODE":                           "oidc",
			"AUTH_ISSUER":                         "http://dex:5556/dex",
			"AUTH_AUDIENCE":                       "open-rtls-cli",
			"AUTH_ALLOWED_ALGS":                   "RS256",
			"AUTH_PERMISSIONS_FILE":               "/app/config/auth/permissions.yaml",
			"AUTH_ROLES_CLAIM":                    "email",
			"AUTH_OWNED_RESOURCES_CLAIM":          "owned_resources",
			"AUTH_OIDC_REFRESH_TTL":               "10m",
			"AUTH_HTTP_TIMEOUT":                   "5s",
			"AUTH_CLOCK_SKEW":                     "30s",
			"OTEL_ENABLED":                        "true",
			"OTEL_METRICS_ENABLED":                "true",
			"OTEL_TRACES_ENABLED":                 "true",
			"OTEL_LOGS_ENABLED":                   "true",
			"OTEL_EXPORTER_OTLP_INSECURE":         "true",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  "http://otlp-sink:8080/v1/traces",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://otlp-sink:8080/v1/metrics",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    "http://otlp-sink:8080/v1/logs",
			// OpenTelemetry's env parser expects millisecond integers here, not Go
			// duration strings such as "2s".
			"OTEL_METRIC_EXPORT_INTERVAL": "2000",
			"OTEL_METRIC_EXPORT_TIMEOUT":  "2000",
			"OTEL_EXPORTER_OTLP_TIMEOUT":  "2000",
			"OTEL_SERVICE_NAME":           "open-rtls-hub-integration",
			"OTEL_DEPLOYMENT_ENVIRONMENT": "integration-test",
		},
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort("8080/tcp").
			WithStartupTimeout(90 * time.Second),
	}
	imageRef, imageErr := sharedHubImageFromRepoRoot()
	if imageErr != nil {
		return nil, fmt.Errorf("docker/app unavailable: %w", imageErr)
	}
	appReq.Image = imageRef
	suite.app, err = startLoggedContainer(ctx, appReq)
	if err != nil {
		return nil, fmt.Errorf("docker/app unavailable: %w; logs: %s", err, containerLogTail(ctx, suite.app))
	}

	suite.appBaseURL, err = mappedHTTPURLNoTest(ctx, suite.app, "8080/tcp")
	if err != nil {
		return nil, err
	}
	suite.brokerURL, err = mappedBrokerURLNoTest(ctx, suite.mosquitto, "1883/tcp")
	if err != nil {
		return nil, err
	}
	suite.adminToken, err = fetchDexIDTokenNoTest(ctx, suite.dex, "admin@example.com", "testpass123")
	if err != nil {
		return nil, err
	}
	suite.readerToken, err = fetchDexIDTokenNoTest(ctx, suite.dex, "reader@example.com", "testpass123")
	if err != nil {
		return nil, err
	}
	suite.ownerToken, err = fetchDexIDTokenNoTest(ctx, suite.dex, "owner@example.com", "testpass123")
	if err != nil {
		return nil, err
	}

	return suite, nil
}

func shutdownPartialSuite(suite *integrationSuite) error {
	if suite == nil {
		return nil
	}
	for _, container := range []testcontainers.Container{suite.app, suite.otlpSink, suite.dex, suite.mosquitto, suite.valkey, suite.postgres} {
		if container != nil {
			_ = container.Terminate(suite.ctx)
		}
	}
	if suite.network != nil {
		_ = suite.network.Remove(suite.ctx)
	}
	return nil
}

func startSuitePostgres(ctx context.Context, networkName string) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:17",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "openrtls",
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": "postgres",
		},
		Networks: []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"postgres"},
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("5432/tcp"),
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		).WithStartupTimeout(30 * time.Second),
	}
	container, err := startLoggedContainer(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("docker/postgres unavailable: %w; logs: %s", err, containerLogTail(ctx, container))
	}
	return container, nil
}

func runSuiteMigrations(ctx context.Context, pg testcontainers.Container) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		exitCode, _, err := pg.Exec(ctx, []string{"pg_isready", "-U", "postgres", "-d", "openrtls"})
		if err == nil && exitCode == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := ensurePostgresDatabaseNoTest(ctx, pg, "openrtls"); err != nil {
		return err
	}
	for _, name := range []string{"00001_initial.sql", "00002_hub_metadata.sql"} {
		content, err := os.ReadFile(filepath.Join(repoRoot(), "migrations", name))
		if err != nil {
			return fmt.Errorf("read migration %s failed: %w", name, err)
		}
		content = gooseUpOnlySQL(content)
		target := "/tmp/" + name
		if err := pg.CopyToContainer(ctx, content, target, 0o644); err != nil {
			return fmt.Errorf("copy migration %s failed: %w", name, err)
		}
		exitCode, output, err := pg.Exec(ctx, []string{"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "openrtls", "-f", target})
		if err != nil {
			return fmt.Errorf("run migration %s failed: %w", name, err)
		}
		if exitCode != 0 {
			data, _ := io.ReadAll(output)
			return fmt.Errorf("migration %s failed with exit code %d: %s", name, exitCode, strings.TrimSpace(string(data)))
		}
	}
	return nil
}

func ensurePostgresDatabaseNoTest(ctx context.Context, pg testcontainers.Container, name string) error {
	exitCode, output, err := pg.Exec(ctx, []string{"psql", "-tA", "-U", "postgres", "-d", "postgres", "-c", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = '%s'", name)})
	if err != nil {
		return fmt.Errorf("check postgres database %s failed: %w", name, err)
	}
	if exitCode != 0 {
		data, _ := io.ReadAll(output)
		return fmt.Errorf("check postgres database %s failed with exit code %d: %s", name, exitCode, strings.TrimSpace(string(data)))
	}
	data, _ := io.ReadAll(output)
	if strings.TrimSpace(string(data)) == "1" {
		return nil
	}
	exitCode, output, err = pg.Exec(ctx, []string{"createdb", "-U", "postgres", name})
	if err != nil {
		return fmt.Errorf("create postgres database %s failed: %w", name, err)
	}
	if exitCode != 0 {
		data, _ := io.ReadAll(output)
		message := strings.TrimSpace(string(data))
		if strings.Contains(message, "already exists") {
			return nil
		}
		return fmt.Errorf("create postgres database %s failed with exit code %d: %s", name, exitCode, message)
	}
	return nil
}

func verifyHubMetadataTableNoTest(ctx context.Context, pg testcontainers.Container) error {
	exitCode, output, err := pg.Exec(ctx, []string{"psql", "-tA", "-U", "postgres", "-d", "openrtls", "-c", "SELECT 1 FROM hub_metadata LIMIT 1"})
	if err != nil {
		return fmt.Errorf("verify hub_metadata table failed: %w", err)
	}
	data, _ := io.ReadAll(output)
	if exitCode != 0 {
		return fmt.Errorf("verify hub_metadata table failed with exit code %d: %s", exitCode, strings.TrimSpace(string(data)))
	}
	return nil
}

func mappedHTTPURLNoTest(ctx context.Context, container testcontainers.Container, port string) (string, error) {
	host, err := container.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("container host lookup failed: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, nat.Port(port))
	if err != nil {
		return "", fmt.Errorf("container port lookup failed: %w", err)
	}
	return fmt.Sprintf("http://%s:%s", host, mappedPort.Port()), nil
}

func mappedBrokerURLNoTest(ctx context.Context, container testcontainers.Container, port string) (string, error) {
	host, err := container.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("mqtt host lookup failed: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, nat.Port(port))
	if err != nil {
		return "", fmt.Errorf("mqtt port lookup failed: %w", err)
	}
	return fmt.Sprintf("tcp://%s:%s", host, mappedPort.Port()), nil
}

func fetchDexIDTokenNoTest(ctx context.Context, container testcontainers.Container, username, password string) (string, error) {
	baseURL, err := mappedHTTPURLNoTest(ctx, container, "5556/tcp")
	if err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("scope", "openid email profile")
	form.Set("username", username)
	form.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/dex/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	req.SetBasicAuth("open-rtls-cli", "cli-secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected dex token status %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode token response failed: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("dex token response did not include access_token")
	}
	return payload.AccessToken, nil
}

func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func sharedHubImageFromRepoRoot() (string, error) {
	hubImageOnce.Do(func() {
		cmd := exec.Command("docker", "build", "-t", hubTestImageRef, repoRoot())
		output, err := cmd.CombinedOutput()
		if err != nil {
			builtErr := buildError(output, err)
			if strings.Contains(builtErr.Error(), `image "docker.io/library/open-rtls-hub-integration-test:local": already exists`) {
				hubImageErr = nil
				return
			}
			hubImageErr = builtErr
		}
	})
	if hubImageErr != nil {
		return "", hubImageErr
	}
	return hubTestImageRef, nil
}

func containerLogTail(ctx context.Context, container testcontainers.Container) string {
	if container == nil {
		return "container not created"
	}
	logs, err := container.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("logs unavailable: %v", err)
	}
	defer logs.Close()
	data, err := io.ReadAll(logs)
	if err != nil {
		return fmt.Sprintf("logs unreadable: %v", err)
	}
	if len(data) == 0 {
		return "logs empty"
	}
	if len(data) > integrationLogTailBytes {
		data = data[len(data)-integrationLogTailBytes:]
	}
	return strings.TrimSpace(string(data))
}

func adminToken(t *testing.T) string {
	t.Helper()
	return requireIntegrationSuite(t).adminToken
}

func authTokens(t *testing.T) (string, string, string) {
	t.Helper()
	suite := requireIntegrationSuite(t)
	return suite.adminToken, suite.readerToken, suite.ownerToken
}

func sharedHub(t *testing.T) (context.Context, string, string) {
	t.Helper()
	suite := requireIntegrationSuite(t)
	return suite.ctx, suite.appBaseURL, suite.brokerURL
}

func scopedID(t *testing.T, prefix string) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.NewReplacer("/", "-", "_", "-", " ", "-").Replace(name)
	return prefix + "-" + name
}
