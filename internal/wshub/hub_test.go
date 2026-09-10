package wshub

import (
	"testing"
	"time"
)

func TestBroadcastDeliversOnlyToSubscribersOfThatTopic(t *testing.T) {
	h := NewHub()
	a := h.Subscribe("topic-a")
	b := h.Subscribe("topic-b")
	defer h.Unsubscribe("topic-a", a)
	defer h.Unsubscribe("topic-b", b)

	h.Broadcast("topic-a", []byte("hello-a"))

	select {
	case msg := <-a:
		if string(msg) != "hello-a" {
			t.Errorf("topic-a subscriber got %q, want %q", msg, "hello-a")
		}
	case <-time.After(time.Second):
		t.Fatal("topic-a subscriber never received its broadcast")
	}

	select {
	case msg := <-b:
		t.Fatalf("topic-b subscriber received a message meant for topic-a: %q (this is exactly the class of bug SEC-11 was — a fragment leaking onto a topic its recipient never subscribed to)", msg)
	case <-time.After(100 * time.Millisecond):
		// correct: no cross-topic delivery
	}
}

func TestUnsubscribeStopsFurtherDelivery(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe("topic")
	h.Unsubscribe("topic", ch)

	h.Broadcast("topic", []byte("after unsubscribe"))

	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after Unsubscribe, not still receiving")
	}
}

func TestBroadcastToTopicWithNoSubscribersDoesNotPanic(t *testing.T) {
	h := NewHub()
	h.Broadcast("nobody-listening", []byte("hello"))
}

func TestSlowSubscriberIsDroppedNotBlocked(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe("topic") // buffered, capacity 16 — see hub.go
	defer h.Unsubscribe("topic", ch)

	done := make(chan struct{})
	go func() {
		// Flood well past the channel's buffer without ever reading it —
		// Broadcast must drop rather than block the publisher goroutine.
		for i := 0; i < 100; i++ {
			h.Broadcast("topic", []byte("msg"))
		}
		close(done)
	}()

	select {
	case <-done:
		// correct: Broadcast never blocked on the full channel
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a slow/non-reading subscriber instead of dropping")
	}
}

func TestMultipleSubscribersToSameTopicAllReceive(t *testing.T) {
	h := NewHub()
	a := h.Subscribe("topic")
	b := h.Subscribe("topic")
	defer h.Unsubscribe("topic", a)
	defer h.Unsubscribe("topic", b)

	h.Broadcast("topic", []byte("fanout"))

	for _, ch := range []chan []byte{a, b} {
		select {
		case msg := <-ch:
			if string(msg) != "fanout" {
				t.Errorf("got %q, want %q", msg, "fanout")
			}
		case <-time.After(time.Second):
			t.Fatal("a subscriber to the same topic never received the broadcast")
		}
	}
}
