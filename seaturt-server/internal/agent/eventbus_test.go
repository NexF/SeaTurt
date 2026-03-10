package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSessionBus_PublishAndSubscribe(t *testing.T) {
	bus := NewSessionBus("sess_1")

	ch1 := bus.Subscribe("sub1")
	ch2 := bus.Subscribe("sub2")

	event := StreamEvent{Type: "text_delta", Data: TextDelta{Content: "hello"}}
	bus.Publish(event)

	// Both subscribers should receive
	select {
	case got := <-ch1:
		if got.Type != "text_delta" {
			t.Errorf("sub1 got type=%q, want text_delta", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("sub1 timed out")
	}

	select {
	case got := <-ch2:
		if got.Type != "text_delta" {
			t.Errorf("sub2 got type=%q, want text_delta", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("sub2 timed out")
	}
}

func TestSessionBus_Snapshot(t *testing.T) {
	bus := NewSessionBus("sess_1")

	// Publish some events
	bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "a"}})
	bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "b"}})
	bus.Publish(StreamEvent{Type: "tool_call", Data: ToolCallEvent{ID: "tc1", Name: "test"}})

	snap := bus.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len=%d, want 3", len(snap))
	}
	if snap[0].Type != "text_delta" || snap[2].Type != "tool_call" {
		t.Errorf("unexpected snapshot content")
	}
}

func TestSessionBus_SnapshotClearedOnDone(t *testing.T) {
	bus := NewSessionBus("sess_1")

	bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "x"}})
	bus.Publish(StreamEvent{Type: "done", Data: nil})

	snap := bus.Snapshot()
	if len(snap) != 0 {
		t.Errorf("snapshot should be empty after done, got len=%d", len(snap))
	}
}

func TestSessionBus_SnapshotClearedOnCancelled(t *testing.T) {
	bus := NewSessionBus("sess_1")

	bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "x"}})
	bus.Publish(StreamEvent{Type: "cancelled", Data: nil})

	snap := bus.Snapshot()
	if len(snap) != 0 {
		t.Errorf("snapshot should be empty after cancelled, got len=%d", len(snap))
	}
}

func TestSessionBus_SnapshotClearedOnError(t *testing.T) {
	bus := NewSessionBus("sess_1")

	bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "x"}})
	bus.Publish(StreamEvent{Type: "error", Data: map[string]string{"message": "fail"}})

	snap := bus.Snapshot()
	if len(snap) != 0 {
		t.Errorf("snapshot should be empty after error, got len=%d", len(snap))
	}
}

func TestSessionBus_Unsubscribe(t *testing.T) {
	bus := NewSessionBus("sess_1")
	ch := bus.Subscribe("sub1")
	bus.Unsubscribe("sub1")

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after unsubscribe")
	}

	if bus.SubscriberCount() != 0 {
		t.Errorf("subscriber count=%d, want 0", bus.SubscriberCount())
	}
}

func TestSessionBus_ConcurrentSafety(t *testing.T) {
	bus := NewSessionBus("sess_1")

	var wg sync.WaitGroup

	// Concurrent subscribes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("sub_%d", n)
			ch := bus.Subscribe(id)
			// Read some events
			go func() {
				for range ch {
				}
			}()
		}(i)
	}

	// Concurrent publishes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "x"}})
		}()
	}

	wg.Wait()

	// Concurrent unsubscribes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bus.Unsubscribe(fmt.Sprintf("sub_%d", n))
		}(i)
	}
	wg.Wait()

	if bus.SubscriberCount() != 0 {
		t.Errorf("subscriber count=%d, want 0", bus.SubscriberCount())
	}
}

func TestSessionBus_SlowConsumerDropsEvents(t *testing.T) {
	bus := NewSessionBus("sess_1")

	// Subscribe but never read
	bus.Subscribe("slow")

	// Publish more events than the channel buffer
	for i := 0; i < sessionBusChannelBuffer+50; i++ {
		bus.Publish(StreamEvent{Type: "text_delta", Data: TextDelta{Content: "x"}})
	}

	// Should not panic or block — events are dropped for slow consumer
	if bus.SubscriberCount() != 1 {
		t.Errorf("subscriber count=%d, want 1", bus.SubscriberCount())
	}
}

func TestGlobalBus_PublishAndSubscribe(t *testing.T) {
	bus := NewGlobalBus()

	ch := bus.Subscribe("sub1")

	event := AgentEvent{
		Type:    "session_created",
		AgentID: "agent_1",
		Data:    map[string]string{"session_id": "sess_1"},
	}
	bus.Publish(event)

	select {
	case got := <-ch:
		if got.Type != "session_created" {
			t.Errorf("got type=%q, want session_created", got.Type)
		}
		if got.AgentID != "agent_1" {
			t.Errorf("got agent_id=%q, want agent_1", got.AgentID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestGlobalBus_MultipleSubscribers(t *testing.T) {
	bus := NewGlobalBus()

	ch1 := bus.Subscribe("sub1")
	ch2 := bus.Subscribe("sub2")
	ch3 := bus.Subscribe("sub3")

	bus.Publish(AgentEvent{Type: "session_created", AgentID: "a1"})

	for _, ch := range []chan AgentEvent{ch1, ch2, ch3} {
		select {
		case got := <-ch:
			if got.Type != "session_created" {
				t.Errorf("got type=%q, want session_created", got.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	}
}

func TestGlobalBus_Unsubscribe(t *testing.T) {
	bus := NewGlobalBus()
	ch := bus.Subscribe("sub1")
	bus.Unsubscribe("sub1")

	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed")
	}
	if bus.SubscriberCount() != 0 {
		t.Errorf("subscriber count=%d, want 0", bus.SubscriberCount())
	}
}

func TestEventHub_GetOrCreateSessionBus(t *testing.T) {
	hub := NewEventHub()

	bus1 := hub.GetOrCreateSessionBus("sess_1")
	bus2 := hub.GetOrCreateSessionBus("sess_1")

	if bus1 != bus2 {
		t.Error("expected same bus instance for same session")
	}

	bus3 := hub.GetOrCreateSessionBus("sess_2")
	if bus1 == bus3 {
		t.Error("expected different bus for different session")
	}
}

func TestEventHub_GetSessionBus(t *testing.T) {
	hub := NewEventHub()

	if hub.GetSessionBus("nonexistent") != nil {
		t.Error("expected nil for nonexistent session")
	}

	hub.GetOrCreateSessionBus("sess_1")
	if hub.GetSessionBus("sess_1") == nil {
		t.Error("expected non-nil for created session")
	}
}

func TestEventHub_RemoveSessionBus(t *testing.T) {
	hub := NewEventHub()
	hub.GetOrCreateSessionBus("sess_1")
	hub.RemoveSessionBus("sess_1")

	if hub.GetSessionBus("sess_1") != nil {
		t.Error("expected nil after removal")
	}
}

func TestEventHub_GlobalBus(t *testing.T) {
	hub := NewEventHub()

	gb := hub.Global()
	if gb == nil {
		t.Fatal("global bus is nil")
	}

	ch := gb.Subscribe("test")
	gb.Publish(AgentEvent{Type: "test_event", AgentID: "a1"})

	select {
	case got := <-ch:
		if got.Type != "test_event" {
			t.Errorf("got type=%q, want test_event", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}
