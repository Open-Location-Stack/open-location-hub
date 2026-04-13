package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func init() {
	configureTestcontainersDockerEnv()
}

func TestMain(m *testing.M) {
	configureTestcontainersDockerEnv()
	code := m.Run()
	if err := shutdownIntegrationSuite(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "integration suite shutdown failed: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func configureTestcontainersDockerEnv() {
	host := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if host == "" {
		host = dockerContextHost()
		if host != "" {
			_ = os.Setenv("DOCKER_HOST", host)
		}
	}
	if strings.HasPrefix(host, "unix://") && host != "unix:///var/run/docker.sock" {
		if os.Getenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE") == "" {
			_ = os.Setenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock")
		}
	}
}

func dockerContextHost() string {
	ctx := strings.TrimSpace(os.Getenv("DOCKER_CONTEXT"))
	if ctx == "" {
		out, err := exec.Command("docker", "context", "show").Output()
		if err != nil {
			return ""
		}
		ctx = strings.TrimSpace(string(out))
	}
	if ctx == "" {
		return ""
	}
	var stdout bytes.Buffer
	cmd := exec.Command("docker", "context", "inspect", ctx, "--format", "{{ (index .Endpoints \"docker\").Host }}")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
