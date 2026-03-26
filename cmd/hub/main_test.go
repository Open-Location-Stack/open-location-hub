package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/hub"
	"go.uber.org/zap"
)

func TestRunEventPublisherPublishesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan hub.Event, 1)
	donePublishing := make(chan struct{}, 1)
	var (
		mu        sync.Mutex
		published []hub.Event
	)

	done := runEventPublisher(ctx, zap.NewNop(), events, func(_ context.Context, event hub.Event) error {
		mu.Lock()
		published = append(published, event)
		mu.Unlock()
		donePublishing <- struct{}{}
		return nil
	})

	expected := hub.Event{Kind: hub.EventLocation, ProviderID: "provider-1"}
	events <- expected

	select {
	case <-donePublishing:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event publication")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for publisher shutdown")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(published))
	}
	if published[0].Kind != expected.Kind || published[0].ProviderID != expected.ProviderID {
		t.Fatalf("published event mismatch: got %+v want %+v", published[0], expected)
	}
}

func TestRunEventPublisherStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan hub.Event)

	done := runEventPublisher(ctx, zap.NewNop(), events, func(_ context.Context, event hub.Event) error {
		t.Fatalf("unexpected published event: %+v", event)
		return nil
	})

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for publisher shutdown after context cancellation")
	}
}
