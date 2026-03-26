package integration

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

var (
	hubImageOnce sync.Once
	hubImageErr  error
)

const hubTestImageRef = "open-rtls-hub-integration-test:local"

func sharedHubImage(t *testing.T) string {
	t.Helper()

	hubImageOnce.Do(func() {
		cmd := exec.Command("docker", "build", "-t", hubTestImageRef, repoPath(t, "."))
		output, err := cmd.CombinedOutput()
		if err != nil {
			hubImageErr = buildError(output, err)
		}
	})
	if hubImageErr != nil {
		t.Fatalf("docker/app unavailable: %v", hubImageErr)
	}
	return hubTestImageRef
}

func buildError(output []byte, err error) error {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return err
	}
	return &buildFailure{message: text, cause: err}
}

type buildFailure struct {
	message string
	cause   error
}

func (e *buildFailure) Error() string {
	return e.message
}

func (e *buildFailure) Unwrap() error {
	return e.cause
}
