package mqtt

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/eclipse/paho.golang/packets"
)

func TestInboundDispatchIsBoundedAndCanceledOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dispatcher := newMessageDispatcher(context.Background(), nil, HandlerConfig{Workers: 2, Buffer: 3})
		client := &Client{dispatcher: dispatcher}
		defer client.Close()
		var started, finished atomic.Int32
		if err := client.Subscribe("topic", func(ctx context.Context, _ string, _ []byte) error {
			started.Add(1)
			<-ctx.Done()
			finished.Add(1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		packet := &packets.Publish{Topic: "topic", Payload: []byte("payload"), Properties: &packets.Properties{}}
		client.router.Route(packet)
		client.router.Route(packet)
		synctest.Wait()
		for range 10 {
			client.router.Route(packet)
		}
		synctest.Wait()
		if started.Load() != 2 || len(dispatcher.queue) != 3 || dispatcher.dropped.Load() != 7 {
			t.Fatalf("workers=%d queued=%d dropped=%d", started.Load(), len(dispatcher.queue), dispatcher.dropped.Load())
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		client.router.Route(packet)
		synctest.Wait()
		if started.Load() != 2 || finished.Load() != 2 {
			t.Fatal("shutdown did not cancel active handlers or started queued work")
		}
	})
}

func TestQueuedMQTTMessageOwnsPayloadAndPreservesExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dispatcher := newMessageDispatcher(context.Background(), nil, HandlerConfig{Workers: 1, Buffer: 1})
		client := &Client{dispatcher: dispatcher}
		defer client.Close()
		block := make(chan struct{})
		got := make(chan string, 1)
		if err := client.Subscribe("topic", func(ctx context.Context, _ string, payload []byte) error {
			if string(payload) == "first" {
				<-block
				return nil
			}
			if expiry, ok := MessageExpiry(ctx); !ok || expiry != 30*time.Second {
				t.Error("message expiry was lost")
			}
			got <- string(payload)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		client.router.Route(&packets.Publish{Topic: "topic", Payload: []byte("first"), Properties: &packets.Properties{}})
		synctest.Wait()
		expiry := uint32(30)
		payload := []byte("second")
		client.router.Route(&packets.Publish{Topic: "topic", Payload: payload, Properties: &packets.Properties{MessageExpiry: &expiry}})
		copy(payload, "broken")
		close(block)
		synctest.Wait()
		if actual := <-got; actual != "second" {
			t.Fatalf("queued payload changed: %q", actual)
		}
	})
}
