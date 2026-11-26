package hub

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/ingest"
)

func TestStreamIngressValidatesWholeBatchAndWaitsForAppend(t *testing.T) {
	t.Parallel()
	now := time.Now()
	service := &Service{now: func() time.Time { return now }, state: NewProcessingState(func() time.Time { return now })}
	world := "EPSG:4326"
	valid := testLocation(t, &world)
	assets := gen.StringIdList{"asset"}
	valid.Trackables = &assets
	invalid := valid
	invalid.ProviderType = "invalid"
	var observed []ingest.Observation
	service.cfg.AppendObservations = func(_ context.Context, batch []ingest.Observation) error {
		observed = batch
		return errors.New("broker unavailable")
	}
	if err := service.ProcessLocations(context.Background(), []gen.Location{valid, invalid}); err == nil || observed != nil {
		t.Fatal("an invalid batch reached the broker")
	}
	err := service.ProcessLocations(context.Background(), []gen.Location{valid})
	var failure *HTTPError
	if !errors.As(err, &failure) || failure.Status != http.StatusServiceUnavailable || len(observed) != 1 {
		t.Fatalf("expected unconfirmed durable acknowledgement, got %v", err)
	}
	batchItem := observed[0]
	if batchItem.Payload.(gen.Location).TimestampGenerated != nil || batchItem.NormalizedLocation.(gen.Location).TimestampGenerated == nil {
		t.Fatal("normalization changed retry identity or discarded default time")
	}
	service.cfg.AppendObservations = func(context.Context, []ingest.Observation) error { return nil }
	if err := service.ProcessLocations(context.Background(), []gen.Location{valid}); err != nil {
		t.Fatal(err)
	}
	if len(service.processingState().ListLatestLocations()) != 0 {
		t.Fatal("ingest projected local state before consuming the committed record")
	}
}
