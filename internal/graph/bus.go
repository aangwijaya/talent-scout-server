package graph

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// Event is one bus message before persistence. A nil NodeID means a run-level
// event (node_id null on the wire); a nil Attempt means no attempt (attempt
// null on the wire). Model, tokens and duration are recorded for node events.
type Event struct {
	NodeID     *string
	Attempt    *int
	Type       string
	Model      *string
	TokensIn   int
	TokensOut  int
	DurationMS int
	Payload    any
}

// subscriberBuffer bounds how many events a slow subscriber can fall behind
// before events are dropped. Persisted events are recoverable by replay, and
// dropped message.delta chunks are superseded by message.completed, so dropping
// is safe.
const subscriberBuffer = 128

type subscriber struct {
	ch chan domain.NodeEvent
}

// Bus persists every event (except message.delta) to node_events and fans it
// out to subscribers, ordered by the store-assigned seq.
type Bus struct {
	store  store.Store
	mu     sync.RWMutex
	subs   map[string]map[int64]*subscriber
	nextID int64
}

// NewBus returns a bus that persists to st.
func NewBus(st store.Store) *Bus {
	return &Bus{store: st, subs: make(map[string]map[int64]*subscriber)}
}

// Subscribe returns a channel of events for runID and a cancel function. The
// channel is never closed by the bus (cancel only stops delivery); once cancel
// is called and the reader returns, the channel is garbage-collected.
func (b *Bus) Subscribe(runID string) (<-chan domain.NodeEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	sub := &subscriber{ch: make(chan domain.NodeEvent, subscriberBuffer)}
	if b.subs[runID] == nil {
		b.subs[runID] = make(map[int64]*subscriber)
	}
	b.subs[runID][id] = sub
	return sub.ch, func() {
		b.mu.Lock()
		delete(b.subs[runID], id)
		b.mu.Unlock()
	}
}

// Emit persists the event (unless it is message.delta) and publishes it. The
// returned domain.NodeEvent carries the store-assigned seq for persisted events.
func (b *Bus) Emit(ctx context.Context, runID string, evt Event) (domain.NodeEvent, error) {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return domain.NodeEvent{}, err
	}

	ne := domain.NodeEvent{
		RunID:      runID,
		NodeID:     evt.NodeID,
		Type:       evt.Type,
		Attempt:    evt.Attempt,
		Model:      evt.Model,
		TokensIn:   evt.TokensIn,
		TokensOut:  evt.TokensOut,
		DurationMS: evt.DurationMS,
		Payload:    payload,
	}

	if evt.Type == EventMessageDelta {
		b.publish(runID, ne)
		return ne, nil
	}

	saved, err := b.store.AppendNodeEvent(ctx, ne)
	if err != nil {
		return domain.NodeEvent{}, err
	}
	b.publish(runID, saved)
	return saved, nil
}

func (b *Bus) publish(runID string, evt domain.NodeEvent) {
	b.mu.RLock()
	chans := make([]chan domain.NodeEvent, 0, len(b.subs[runID]))
	for _, sub := range b.subs[runID] {
		chans = append(chans, sub.ch)
	}
	b.mu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- evt:
		default: // drop; recoverable via replay / message.completed
		}
	}
}
