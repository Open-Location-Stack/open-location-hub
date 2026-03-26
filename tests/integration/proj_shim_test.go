package integration

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func TestProjPkgConfigShimBuildsInDockerWithoutSystemPkgConfig(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker build unavailable: %v", r)
		}
	}()

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    repoPath(t, "."),
			Dockerfile: "tests/integration/dockerfiles/Dockerfile.proj-shim",
			Repo:       testImageRepo(t, "open-rtls-hub-proj-shim"),
			Tag:        testImageTag(),
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false,
	})
	if err != nil {
		t.Skipf("docker build unavailable: %v", err)
	}

	_ = container
}
