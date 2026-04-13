package hub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/formation-res/open-rtls-hub/internal/observability"
	"go.uber.org/zap"
)

func TestDecisionQueueProcessorDrainsBatch(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service := &Service{
		logger:           zap.NewNop(),
		stats:            newRuntimeStats(),
		telemetryRuntime: observability.Global(),
	}

	var (
		mu         sync.Mutex
		batchSizes []int
		done       = make(chan struct{}, 1)
	)

	processor := &derivedLocationProcessor{
		service: service,
		logger:  service.logger,
		queue:   make(chan derivedLocationWork, 8),
		label:   "decision location queue",
		onProcessBatch: func(_ context.Context, batch []derivedLocationWork) error {
			mu.Lock()
			batchSizes = append(batchSizes, len(batch))
			mu.Unlock()
			select {
			case done <- struct{}{}:
			default:
			}
			return nil
		},
		maxBatchSize: 4,
	}

	go processor.run(ctx)

	for i := 0; i < 3; i++ {
		processor.Submit(derivedLocationWork{
			Context:    context.Background(),
			Location:   gen.Location{ProviderId: "provider-a", Source: "source-a"},
			EnqueuedAt: time.Now(),
		})
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batch processing")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batchSizes) == 0 {
		t.Fatal("expected at least one batch")
	}
	if batchSizes[0] != 3 {
		t.Fatalf("expected first batch size 3, got %d", batchSizes[0])
	}
}
