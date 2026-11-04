package events

import (
	"encoding/json"
	"sync"

	"github.com/potibm/s3hog/internal/model"
)

// Broker distributes events to SSE subscribers.
type Broker struct {
	mu   sync.Mutex
	subs map[chan model.Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[chan model.Event]struct{})}
}

// Subscribe returns a channel that receives published events.
func (b *Broker) Subscribe() chan model.Event {
	ch := make(chan model.Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *Broker) Unsubscribe(ch chan model.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}

// Publish sends an event to all subscribers (non-blocking).
func (b *Broker) Publish(e model.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Marshal returns the JSON encoding of an event.
func Marshal(e model.Event) []byte {
	b, _ := json.Marshal(e)
	return b
}
