package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type sseEvent struct {
	Name string
	Data []byte
}

type broker struct {
	mu        sync.Mutex
	clients   map[chan sseEvent]struct{}
	ring      []sseEvent
	ringLimit int
	clientBuf int
	closed    bool
}

func newBroker(clientBuf, ringLimit int) *broker {
	return &broker{
		clients:   make(map[chan sseEvent]struct{}),
		ringLimit: ringLimit,
		clientBuf: clientBuf,
	}
}

func (b *broker) Publish(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	ev := sseEvent{Name: name, Data: data}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if name == "change" {
		b.ring = append(b.ring, ev)
		if len(b.ring) > b.ringLimit {
			b.ring = b.ring[len(b.ring)-b.ringLimit:]
		}
	}
	for ch := range b.clients {
		select {
		case ch <- ev:
		default:
			// A slow browser is allowed to miss events; it will reconcile
			// open files through the normal API on its next interaction.
		}
	}
}

// Subscribe atomically registers a client and snapshots the replay ring, so
// there is no gap or duplicate between replay and live events.
func (b *broker) Subscribe() (chan sseEvent, []sseEvent) {
	ch := make(chan sseEvent, b.clientBuf)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(ch)
		return ch, nil
	}
	backlog := make([]sseEvent, len(b.ring))
	copy(backlog, b.ring)
	b.clients[ch] = struct{}{}
	return ch, backlog
}

func (b *broker) Unsubscribe(ch chan sseEvent) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *broker) Recent() []sseEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]sseEvent, len(b.ring))
	copy(out, b.ring)
	return out
}

func (b *broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.clients {
		close(ch)
		delete(b.clients, ch)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, backlog := s.broker.Subscribe()
	defer s.broker.Unsubscribe(ch)

	ready, _ := json.Marshal(map[string]any{
		"at":      time.Now().UTC(),
		"root":    s.ws.Root(),
		"version": s.info().Version,
	})
	fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready)
	for _, ev := range backlog {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data)
	}
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
