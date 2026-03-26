package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
)

const testRequestBodyLimitBytes int64 = 64

func TestDecodeJSONBodyAcceptsSingleDocument(t *testing.T) {
	req := httptest.NewRequest("POST", "/v2/trackables", strings.NewReader(`{"type":"asset"}`))
	rec := httptest.NewRecorder()

	var body gen.TrackableWrite
	err := decodeJSONBody(rec, req, testRequestBodyLimitBytes, &body)
	if err != nil {
		t.Fatalf("decodeJSONBody returned error: %v", err)
	}
	if body.Type != "asset" {
		t.Fatalf("unexpected type: %q", body.Type)
	}
}

func TestDecodeJSONBodyRejectsTrailingJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/v2/trackables", strings.NewReader(`{"type":"asset"} {"extra":true}`))
	rec := httptest.NewRecorder()

	var body gen.TrackableWrite
	err := decodeJSONBody(rec, req, testRequestBodyLimitBytes, &body)
	if err == nil {
		t.Fatal("expected decodeJSONBody to reject trailing JSON")
	}
}

func TestDecodeJSONBodyRejectsOversizedBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/v2/trackables", strings.NewReader(`{"type":"asset","properties":{"payload":"abcdefghijklmnopqrstuvwxyz"}}`))
	rec := httptest.NewRecorder()

	var body gen.TrackableWrite
	err := decodeJSONBody(rec, req, 32, &body)
	if err == nil {
		t.Fatal("expected decodeJSONBody to reject oversized body")
	}
}

func TestDecodeJSONBodyAllowsUnknownFields(t *testing.T) {
	req := httptest.NewRequest("POST", "/v2/trackables", strings.NewReader(`{"type":"asset","unknown":true}`))
	rec := httptest.NewRecorder()

	var body gen.TrackableWrite
	err := decodeJSONBody(rec, req, testRequestBodyLimitBytes, &body)
	if err != nil {
		t.Fatalf("decodeJSONBody returned error: %v", err)
	}
	if body.Type != "asset" {
		t.Fatalf("unexpected type: %q", body.Type)
	}
}

func TestReadRawBodyAcceptsArbitraryJSON(t *testing.T) {
	input := `{"type":"Polygon","coordinates":[[[1,2],[3,4],[1,2]]],"extension":{"unknown":true}}`
	req := httptest.NewRequest("POST", "/v2/fences", strings.NewReader(input))
	rec := httptest.NewRecorder()

	raw, err := readRawBody(rec, req, 256)
	if err != nil {
		t.Fatalf("readRawBody returned error: %v", err)
	}

	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal raw body failed: %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatalf("unmarshal input failed: %v", err)
	}
	if !jsonEqual(got, want) {
		t.Fatal("raw JSON body did not round-trip")
	}
}

func jsonEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}
