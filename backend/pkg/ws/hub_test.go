package ws

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestHubPublishesOnlyToTargetUser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := NewHub()
	go hub.Run(ctx)

	target := &client{userID: 7, send: make(chan []byte, 1)}
	other := &client{userID: 8, send: make(chan []byte, 1)}
	hub.register <- target
	hub.register <- other
	hub.Publish(7, Event{Event: "paid", OrderID: 99, Status: "paid"})

	select {
	case payload := <-target.send:
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.OrderID != 99 || event.Event != "paid" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("target user did not receive event")
	}

	select {
	case <-other.send:
		t.Fatal("event leaked to another user")
	case <-time.After(20 * time.Millisecond):
	}
}
