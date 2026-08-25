// Package wshub is a minimal topic-based pub/sub hub for pushing
// server-rendered HTML fragments to connected websocket clients.
package wshub

import "sync"

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]bool
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan []byte]bool)}
}

// Subscribe registers a new listener on topic and returns the channel it
// will receive broadcasts on. The channel is buffered so a slow reader
// doesn't block publishers; Broadcast drops messages for a full channel.
func (h *Hub) Subscribe(topic string) chan []byte {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[topic] == nil {
		h.subs[topic] = make(map[chan []byte]bool)
	}
	h.subs[topic][ch] = true
	return ch
}

func (h *Hub) Unsubscribe(topic string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[topic][ch]; !ok {
		return
	}
	delete(h.subs[topic], ch)
	if len(h.subs[topic]) == 0 {
		delete(h.subs, topic)
	}
	close(ch)
}

func (h *Hub) Broadcast(topic string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- msg:
		default:
			// Slow consumer; drop rather than block the broadcaster.
		}
	}
}
