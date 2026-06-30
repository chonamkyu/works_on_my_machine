package store

import (
	"testing"
	"time"
)

// TestBus_WorkspaceIsolation verifies that events published with UserID="wsB"
// are NOT delivered to a subscriber that filters by UserID="wsA", but ARE
// delivered to one filtering by UserID="wsB".
//
// This exercises the real Subscribe/Publish path (no mocking of Bus).
// The workspace-scoped filter lives in the SSE handler fan-out goroutines
// (handler.go ActivityStream), not in Bus.fanout itself. Bus.fanout delivers
// all channel-matching events; the SSE layer then drops events whose
// UserID != subscriberWorkspace. We replicate that exact filter here.
func TestBus_WorkspaceIsolation(t *testing.T) {
	bus := NewBus()

	const (
		wsA = "workspace-A"
		wsB = "workspace-B"
	)

	// Subscribe both workspaces to the same broad channel pattern.
	rawCh, cancel := bus.Subscribe("task:created:*")
	defer cancel()

	// Simulate the per-workspace filter that ActivityStream applies:
	// a goroutine reads from rawCh and forwards only if ev.UserID matches.
	filteredA := make(chan Event, 8)
	filteredB := make(chan Event, 8)
	go func() {
		for ev := range rawCh {
			if ev.UserID == "" || ev.UserID == wsA {
				filteredA <- ev
			}
			if ev.UserID == "" || ev.UserID == wsB {
				filteredB <- ev
			}
		}
	}()

	// Publish an event scoped to workspace B.
	bus.Publish(Event{
		Channel: "task:created:t_abc",
		UserID:  wsB,
		Payload: map[string]any{"task": "t_abc"},
	})

	// wsB subscriber MUST receive the event.
	select {
	case ev := <-filteredB:
		if ev.UserID != wsB {
			t.Fatalf("wsB subscriber got event with UserID=%q, want %q", ev.UserID, wsB)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("wsB subscriber did not receive its own event within 200ms")
	}

	// wsA subscriber must NOT receive wsB's event.
	select {
	case ev := <-filteredA:
		t.Fatalf("wsA subscriber received event that belongs to wsB (UserID=%q)", ev.UserID)
	case <-time.After(100 * time.Millisecond):
		// expected: no event leaked to wsA
	}

	// An event with no UserID (broadcast) reaches both workspaces.
	bus.Publish(Event{
		Channel: "task:created:t_broadcast",
		UserID:  "", // no workspace scope — broadcast
	})

	drain := func(ch <-chan Event, label string) {
		select {
		case ev := <-ch:
			if ev.Channel != "task:created:t_broadcast" {
				t.Errorf("%s: unexpected channel %q in broadcast event", label, ev.Channel)
			}
		case <-time.After(200 * time.Millisecond):
			t.Errorf("%s: did not receive broadcast event within 200ms", label)
		}
	}
	drain(filteredA, "wsA")
	drain(filteredB, "wsB")
}

// TestBus_ExactChannelIsolation verifies that a subscriber on an exact channel
// only receives events on that exact channel — not events for a different
// entity ID on the same channel family.
func TestBus_ExactChannelIsolation(t *testing.T) {
	bus := NewBus()

	ch1, cancel1 := bus.Subscribe("run:state:run-001")
	defer cancel1()
	ch2, cancel2 := bus.Subscribe("run:state:run-002")
	defer cancel2()

	// Publish to run-002 only.
	bus.Publish(Event{
		Channel: "run:state:run-002",
		UserID:  "ws-x",
		Payload: map[string]any{"state": "running"},
	})

	// run-001 subscriber must NOT receive it.
	select {
	case ev := <-ch1:
		t.Fatalf("run-001 subscriber received event for run-002 (channel=%q)", ev.Channel)
	case <-time.After(100 * time.Millisecond):
		// correct
	}

	// run-002 subscriber MUST receive it.
	select {
	case ev := <-ch2:
		if ev.Channel != "run:state:run-002" {
			t.Fatalf("wrong channel: %q", ev.Channel)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("run-002 subscriber did not receive its event")
	}
}
