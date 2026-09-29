package hub

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

func TestEventBusCoalescesLaggingLocationEvents(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(1)
	defer unsubscribe()

	first, err := newEvent(
		EventLocation,
		ScopeEPSG4326,
		time.Now(),
		"provider-a",
		"",
		"",
		"hub-a",
		LocationEnvelope{Location: gen.Location{ProviderId: "provider-a", Source: "source-a"}},
	)
	if err != nil {
		t.Fatalf("first event: %v", err)
	}
	second, err := newEvent(
		EventLocation,
		ScopeEPSG4326,
		time.Now().Add(time.Millisecond),
		"provider-a",
		"",
		"",
		"hub-a",
		LocationEnvelope{Location: gen.Location{ProviderId: "provider-a", Source: "source-a"}},
	)
	if err != nil {
		t.Fatalf("second event: %v", err)
	}
	third, err := newEvent(
		EventLocation,
		ScopeEPSG4326,
		time.Now().Add(2*time.Millisecond),
		"provider-a",
		"",
		"",
		"hub-a",
		LocationEnvelope{Location: gen.Location{ProviderId: "provider-a", Source: "source-a"}},
	)
	if err != nil {
		t.Fatalf("third event: %v", err)
	}

	bus.Emit(first)
	bus.Emit(second)
	bus.Emit(third)

	gotFirst := <-ch
	if gotFirst.EventTime != first.EventTime {
		t.Fatalf("expected first queued event, got %v", gotFirst.EventTime)
	}

	select {
	case gotSecond := <-ch:
		if gotSecond.EventTime != third.EventTime {
			t.Fatalf("expected latest coalesced event, got %v want %v", gotSecond.EventTime, third.EventTime)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coalesced event")
	}

	if drops := bus.Stats().Snapshot().EventBusDrops; drops != 0 {
		t.Fatalf("expected no event bus drops for coalesced locations, got %d", drops)
	}
}

func TestEventBusStillDropsDiscreteEventsWhenSubscriberIsFull(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ch, unsubscribe := bus.Subscribe(1)
	defer unsubscribe()

	first, err := newEvent(
		EventFenceEvent,
		ScopeDerived,
		time.Now(),
		"provider-a",
		"trackable-a",
		"fence-a",
		"hub-a",
		FenceEventEnvelope{},
	)
	if err != nil {
		t.Fatalf("first event: %v", err)
	}
	second, err := newEvent(
		EventFenceEvent,
		ScopeDerived,
		time.Now().Add(time.Millisecond),
		"provider-a",
		"trackable-a",
		"fence-a",
		"hub-a",
		FenceEventEnvelope{},
	)
	if err != nil {
		t.Fatalf("second event: %v", err)
	}

	bus.Emit(first)
	bus.Emit(second)

	<-ch
	if drops := bus.Stats().Snapshot().EventBusDrops; drops != 1 {
		t.Fatalf("expected one event bus drop for discrete events, got %d", drops)
	}
}

func TestEventBusFlushesAfterIdleSubscriberResumes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewEventBus()
		ch, unsubscribe := bus.Subscribe(1)
		defer unsubscribe()
		event := Event{Kind: EventLocation, Payload: LocationEnvelope{Location: gen.Location{ProviderId: "provider"}}}
		bus.Emit(event)
		event.EventTime = time.Now()
		bus.Emit(event)
		// Let the flusher encounter a full channel with no new messages coming.
		synctest.Wait()
		event.EventTime = event.EventTime.Add(time.Second)
		bus.Emit(event)
		synctest.Wait()
		<-ch
		synctest.Wait()
		select {
		case latest := <-ch:
			if !latest.EventTime.Equal(event.EventTime) {
				t.Fatal("pending update was not refreshed while waiting for the reader")
			}
		default:
			t.Fatal("pending event remained stuck after the reader resumed")
		}
	})
}

func TestEventBusConcurrentSubscribeEmitAndUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	event := Event{Kind: EventLocation, Payload: LocationEnvelope{Location: gen.Location{ProviderId: "provider"}}}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				ch, unsubscribe := bus.Subscribe(1)
				bus.Emit(event)
				bus.Emit(event)
				unsubscribe()
				unsubscribe()
				for range ch {
				}
			}
		})
	}
	workers.Wait()
	if got := bus.Stats().Snapshot().EventBusSubscribers; got != 0 {
		t.Fatalf("subscriber count after cleanup = %d", got)
	}
}
