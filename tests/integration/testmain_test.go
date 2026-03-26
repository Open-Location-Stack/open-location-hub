package integration

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

func init() {
	configureTestcontainersDockerEnv()
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
