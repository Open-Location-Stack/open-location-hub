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
)

func TestObservabilityExportsOTLPLogsMetricsAndTraces(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker/testcontainers unavailable: %v", r)
		}
	}()

	suite := requireIntegrationSuite(t)
	ctx, appBaseURL, _ := sharedHub(t)
	token := adminToken(t)
	payload := georeferencedZonePayload(0.5, 0.5, false)
	payload["name"] = scopedID(t, "observability-zone")
	createResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/zones", token, payload)
	assertStatus(t, createResp, http.StatusCreated)
	var zone struct {
		ID string `json:"id"`
	}
	decodeResponse(t, createResp, &zone)

	locationResp := requestJSON(t, http.MethodPost, appBaseURL+"/v2/providers/locations", token, []map[string]any{{
		"crs":           "local",
		"position":      pointPayload(5, 7),
		"provider_id":   scopedID(t, "provider-observability"),
		"provider_type": "uwb",
		"source":        zone.ID,
	}})
	assertStatusAndClose(t, locationResp, http.StatusAccepted)

	if err := waitForSinkPaths(ctx, suite.otlpSink, 30*time.Second, "/v1/traces", "/v1/metrics", "/v1/logs"); err != nil {
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
