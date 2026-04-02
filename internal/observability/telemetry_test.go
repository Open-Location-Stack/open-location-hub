package observability

import "testing"

func TestEndpointOptionTreatsBaseURLAsEndpointHost(t *testing.T) {
	got := endpointOption("http://collector:4318", func(v string) string {
		return "raw:" + v
	}, func(v string) string {
		return "url:" + v
	})

	if got != "raw:collector:4318" {
		t.Fatalf("expected raw endpoint host, got %q", got)
	}
}

func TestEndpointOptionPreservesExplicitSignalPathURL(t *testing.T) {
	got := endpointOption("http://collector:4318/v1/logs", func(v string) string {
		return "raw:" + v
	}, func(v string) string {
		return "url:" + v
	})

	if got != "url:http://collector:4318/v1/logs" {
		t.Fatalf("expected explicit URL path to be preserved, got %q", got)
	}
}
