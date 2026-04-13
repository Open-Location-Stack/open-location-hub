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
	"github.com/testcontainers/testcontainers-go/wait"
)

const integrationLogTailBytes = 16 * 1024

func TestDexBackedAuthorization(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	_, appBaseURL, _ := sharedHub(t)
	adminToken, readerToken, ownerToken := authTokens(t)
	zoneName := scopedID(t, "test-zone")

	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/zones", ""), http.StatusUnauthorized)
	assertStatusAndClose(t, request(t, http.MethodGet, appBaseURL+"/v2/zones", "definitely-not-a-jwt"), http.StatusUnauthorized)

	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", adminToken, map[string]any{
		"type":                     "uwb",
		"incomplete_configuration": true,
		"ground_control_points":    []map[string]any{},
		"name":                     zoneName,
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
	ensurePostgresDatabase(t, ctx, pg, "openrtls")
	for _, name := range []string{"00001_initial.sql", "00002_hub_metadata.sql"} {
		content, err := os.ReadFile(repoPath(t, filepath.Join("migrations", name)))
		if err != nil {
			t.Fatalf("read migration %s failed: %v", name, err)
		}
		content = gooseUpOnlySQL(content)
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

func gooseUpOnlySQL(content []byte) []byte {
	text := string(content)
	if idx := strings.Index(text, "\n-- +goose Down"); idx >= 0 {
		text = text[:idx]
	}
	return []byte(text)
}

func startLoggedContainer(ctx context.Context, req testcontainers.ContainerRequest) (testcontainers.Container, error) {
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false,
	})
	if err != nil {
		return nil, err
	}
	if err := container.Start(ctx); err != nil {
		return container, err
	}
	return container, nil
}

func logContainerOutput(t *testing.T, ctx context.Context, name string, container testcontainers.Container) {
	t.Helper()
	if container == nil {
		return
	}
	logs, err := container.Logs(ctx)
	if err != nil {
		t.Logf("%s logs unavailable: %v", name, err)
		return
	}
	defer logs.Close()

	data, err := io.ReadAll(logs)
	if err != nil {
		t.Logf("%s logs unreadable: %v", name, err)
		return
	}
	if len(data) == 0 {
		t.Logf("%s logs empty", name)
		return
	}
	if len(data) > integrationLogTailBytes {
		data = data[len(data)-integrationLogTailBytes:]
	}
	t.Logf("%s logs (tail):\n%s", name, strings.TrimSpace(string(data)))
}

func ensurePostgresDatabase(t *testing.T, ctx context.Context, pg testcontainers.Container, name string) {
	t.Helper()
	exitCode, output, err := pg.Exec(ctx, []string{"psql", "-tA", "-U", "postgres", "-d", "postgres", "-c", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = '%s'", name)})
	if err != nil {
		t.Fatalf("check postgres database %s failed: %v", name, err)
	}
	if exitCode != 0 {
		data, _ := io.ReadAll(output)
		t.Fatalf("check postgres database %s failed with exit code %d: %s", name, exitCode, strings.TrimSpace(string(data)))
	}
	data, _ := io.ReadAll(output)
	if strings.TrimSpace(string(data)) == "1" {
		return
	}
	exitCode, output, err = pg.Exec(ctx, []string{"createdb", "-U", "postgres", name})
	if err != nil {
		t.Fatalf("create postgres database %s failed: %v", name, err)
	}
	if exitCode != 0 {
		data, _ := io.ReadAll(output)
		message := strings.TrimSpace(string(data))
		if strings.Contains(message, "already exists") {
			return
		}
		t.Fatalf("create postgres database %s failed with exit code %d: %s", name, exitCode, message)
	}
}

func startPostgres(t *testing.T, ctx context.Context, networkName string) (testcontainers.Container, error) {
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
		// Wait for the port and for the second readiness log line so initialization
		// has completed before tests exec psql/createdb inside the container.
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("5432/tcp"),
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		).WithStartupTimeout(30 * time.Second),
	}
	container, err := startLoggedContainer(ctx, req)
	if err != nil {
		logContainerOutput(t, ctx, "postgres", container)
		return nil, err
	}
	return container, nil
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
