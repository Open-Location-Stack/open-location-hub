package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestDexBackedAuthorization(t *testing.T) {
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

	pg, err := startPostgres(ctx, network.Name)
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

	dexReq := testcontainers.ContainerRequest{
		Image:        "ghcr.io/dexidp/dex:v2.43.1",
		ExposedPorts: []string{"5556/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"dex"},
		},
		Cmd: []string{"dex", "serve", "/etc/dex/config.yaml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      repoPath(t, "tools/dex/config.yaml"),
			ContainerFilePath: "/etc/dex/config.yaml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForHTTP("/dex/.well-known/openid-configuration").
			WithPort("5556/tcp").
			WithStartupTimeout(30 * time.Second),
	}
	dex, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: dexReq,
		Started:          true,
	})
	if err != nil {
		t.Skipf("docker/dex unavailable: %v", err)
	}
	t.Cleanup(func() { _ = dex.Terminate(ctx) })

	runMigrations(t, ctx, pg)

	appReq := testcontainers.ContainerRequest{
		Image:        sharedHubImage(t),
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{network.Name},
		NetworkAliases: map[string][]string{
			network.Name: {"hub"},
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      repoPath(t, "config/auth/permissions.yaml"),
			ContainerFilePath: "/app/config/auth/permissions.yaml",
			FileMode:          0o644,
		}},
		Env: map[string]string{
			"HTTP_LISTEN_ADDR":           ":8080",
			"POSTGRES_URL":               "postgres://postgres:postgres@postgres:5432/openrtls?sslmode=disable",
			"VALKEY_URL":                 "redis://valkey:6379/0",
			"MQTT_BROKER_URL":            "tcp://mosquitto:1883",
			"AUTH_MODE":                  "oidc",
			"AUTH_ISSUER":                "http://dex:5556/dex",
			"AUTH_AUDIENCE":              "open-rtls-cli",
			"AUTH_ALLOWED_ALGS":          "RS256",
			"AUTH_PERMISSIONS_FILE":      "/app/config/auth/permissions.yaml",
			"AUTH_ROLES_CLAIM":           "email",
			"AUTH_OWNED_RESOURCES_CLAIM": "owned_resources",
			"AUTH_OIDC_REFRESH_TTL":      "10m",
			"AUTH_HTTP_TIMEOUT":          "5s",
			"AUTH_CLOCK_SKEW":            "30s",
		},
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort("8080/tcp").
			WithStartupTimeout(60 * time.Second),
	}
	app, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: appReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("docker/app unavailable: %v", err)
	}
	t.Cleanup(func() { _ = app.Terminate(ctx) })

	adminToken := fetchDexIDToken(t, ctx, dex, "admin@example.com", "testpass123")
	readerToken := fetchDexIDToken(t, ctx, dex, "reader@example.com", "testpass123")
	ownerToken := fetchDexIDToken(t, ctx, dex, "owner@example.com", "testpass123")

	appBaseURL := mappedHTTPURL(t, ctx, app, "8080/tcp")

	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/zones", ""), http.StatusUnauthorized)
	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/zones", "definitely-not-a-jwt"), http.StatusUnauthorized)

	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", adminToken, map[string]any{
		"type":                     "uwb",
		"incomplete_configuration": true,
		"ground_control_points":    []map[string]any{},
		"name":                     "Test Zone",
	})
	assertStatus(t, createResp, http.StatusCreated)

	var createdZone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &createdZone)
	if createdZone.ID == "" {
		t.Fatal("expected created zone id")
	}

	listResp := request(t, http.MethodGet, appBaseURL+"/v2/zones", adminToken)
	assertStatus(t, listResp, http.StatusOK)
	var zones []map[string]any
	decodeResponse(t, listResp, &zones)
	if len(zones) == 0 {
		t.Fatal("expected at least one zone")
	}

	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/zones/"+createdZone.ID, adminToken), http.StatusOK)
	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/providers", readerToken), http.StatusForbidden)
	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/providers/provider-1", ownerToken), http.StatusForbidden)
}

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	base := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	return filepath.Join(base, rel)
}

func testImageRepo(t *testing.T, prefix string) string {
	t.Helper()

	name := strings.ToLower(t.Name())
	name = strings.NewReplacer("/", "-", "_", "-", " ", "-").Replace(name)
	return fmt.Sprintf("%s-%s", prefix, name)
}

func testImageTag() string {
	return uuid.NewString()
}

func runMigrations(t *testing.T, ctx context.Context, pg testcontainers.Container) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		exitCode, _, err := pg.Exec(ctx, []string{"pg_isready", "-U", "postgres", "-d", "openrtls"})
		if err == nil && exitCode == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, name := range []string{"00001_initial.sql", "00002_hub_metadata.sql"} {
		content, err := os.ReadFile(repoPath(t, filepath.Join("migrations", name)))
		if err != nil {
			t.Fatalf("read migration %s failed: %v", name, err)
		}
		target := "/tmp/" + name
		if err := pg.CopyToContainer(ctx, content, target, 0o644); err != nil {
			t.Fatalf("copy migration %s failed: %v", name, err)
		}
		exitCode, output, err := pg.Exec(ctx, []string{"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "openrtls", "-f", target})
		if err != nil {
			t.Fatalf("run migration %s failed: %v", name, err)
		}
		if exitCode != 0 {
			data, _ := io.ReadAll(output)
			t.Fatalf("migration %s failed with exit code %d: %s", name, exitCode, strings.TrimSpace(string(data)))
		}
	}
}

func startPostgres(ctx context.Context, networkName string) (testcontainers.Container, error) {
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
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(30 * time.Second),
	}
	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
}

func fetchDexIDToken(t *testing.T, ctx context.Context, container testcontainers.Container, username, password string) string {
	t.Helper()
	baseURL := mappedHTTPURL(t, ctx, container, "5556/tcp")

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("scope", "openid email profile")
	form.Set("username", username)
	form.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/dex/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	req.SetBasicAuth("open-rtls-cli", "cli-secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token exchange failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected dex token status %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode token response failed: %v", err)
	}
	if payload.AccessToken == "" {
		t.Fatal("dex token response did not include access_token")
	}
	return payload.AccessToken
}

func mappedHTTPURL(t *testing.T, ctx context.Context, container testcontainers.Container, port string) string {
	t.Helper()
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host lookup failed: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, nat.Port(port))
	if err != nil {
		t.Fatalf("container port lookup failed: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, mappedPort.Port())
}

func request(t *testing.T, method, target, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func requestJSON(t *testing.T, method, target, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func decodeResponse(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		defer resp.Body.Close()
		t.Fatalf("unexpected status: got %d want %d", resp.StatusCode, want)
	}
}

func assertStatusAndClose(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	assertStatus(t, resp, want)
}
