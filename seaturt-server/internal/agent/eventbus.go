package agent

import (
	"log/slog"
	"sync"
)

// ---- Agent-level event types (for GlobalBus) ----

// AgentEvent represents an agent-level event broadcast via the GlobalBus.
type AgentEvent struct {
	Type    string `json:"type"`     // "session_created", "session_updated", "session_deleted", "cron_execution_started", "cron_execution_finished"
	AgentID string `json:"agent_id"` // which agent this event belongs to
	Data    any    `json:"data"`
}

// ---- SessionBus ----

const (
	// sessionBusChannelBuffer is the buffer size for subscriber channels.
	// Slow consumers will have events dropped (select-default pattern).
	sessionBusChannelBuffer = 256

	// maxSnapshotBuffer limits the snapshot buffer size to prevent unbounded growth.
	maxSnapshotBuffer = 1000
)

// SessionBus manages event broadcast + snapshot buffer for a single session.
type SessionBus struct {
	mu          sync.RWMutex
	sessionID   string
	subscribers map[string]chan StreamEvent // subscriberID -> buffered channel
	buffer      []StreamEvent              // current turn's accumulated events (for snapshot)
}

// NewSessionBus creates a SessionBus for the given session.
func NewSessionBus(sessionID string) *SessionBus {
	return &SessionBus{
		sessionID:   sessionID,
		subscribers: make(map[string]chan StreamEvent),
		buffer:      make([]StreamEvent, 0, 64),
	}
}

// Publish writes an event to the buffer and broadcasts it to all subscribers.
// Done/cancelled events trigger buffer clearing.
func (sb *SessionBus) Publish(event StreamEvent) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Append to snapshot buffer (with size limit)
	if len(sb.buffer) < maxSnapshotBuffer {
		sb.buffer = append(sb.buffer, event)
	}

	// Broadcast to all subscribers (non-blocking, drop if full)
	for id, ch := range sb.subscribers {
		select {
		case ch <- event:
		default:
			slog.Warn("session event dropped (slow consumer)",
				"session_id", sb.sessionID,
				"subscriber", id,
				"event_type", event.Type,
			)
		}
	}

	// Clear buffer on terminal events
	if event.Type == "done" || event.Type == "cancelled" || event.Type == "error" {
		sb.buffer = sb.buffer[:0]
	}
}

// Subscribe registers a new subscriber and returns (channel, subscriberID).
// The returned channel receives events going forward.
func (sb *SessionBus) Subscribe(subscriberID string) chan StreamEvent {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	ch := make(chan StreamEvent, sessionBusChannelBuffer)
	sb.subscribers[subscriberID] = ch
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (sb *SessionBus) Unsubscribe(subscriberID string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if ch, ok := sb.subscribers[subscriberID]; ok {
		close(ch)
		delete(sb.subscribers, subscriberID)
	}
}

// Snapshot returns a copy of the current turn's accumulated events.
// Used when a new subscriber connects mid-execution to catch up.
func (sb *SessionBus) Snapshot() []StreamEvent {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	if len(sb.buffer) == 0 {
		return nil
	}
	snapshot := make([]StreamEvent, len(sb.buffer))
	copy(snapshot, sb.buffer)
	return snapshot
}

// ClearBuffer explicitly clears the snapshot buffer.
func (sb *SessionBus) ClearBuffer() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.buffer = sb.buffer[:0]
}

// SubscriberCount returns the number of active subscribers.
func (sb *SessionBus) SubscriberCount() int {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return len(sb.subscribers)
}

// ---- GlobalBus ----

const globalBusChannelBuffer = 64

// GlobalBus manages broadcast of agent-level events (session_created, etc).
// There is one GlobalBus instance shared across all agents.
type GlobalBus struct {
	mu          sync.RWMutex
	subscribers map[string]chan AgentEvent // subscriberID -> buffered channel
}

// NewGlobalBus creates a new GlobalBus.
func NewGlobalBus() *GlobalBus {
	return &GlobalBus{
		subscribers: make(map[string]chan AgentEvent),
	}
}

// Publish broadcasts an agent-level event to all subscribers.
func (gb *GlobalBus) Publish(event AgentEvent) {
	gb.mu.RLock()
	defer gb.mu.RUnlock()

	slog.Info("[GlobalBus] Publish",
		"event_type", event.Type,
		"agent_id", event.AgentID,
		"subscriber_count", len(gb.subscribers),
	)

	for id, ch := range gb.subscribers {
		select {
		case ch <- event:
			slog.Debug("[GlobalBus] event sent to subscriber", "subscriber", id, "event_type", event.Type)
		default:
			slog.Warn("global event dropped (slow consumer)",
				"subscriber", id,
				"event_type", event.Type,
			)
		}
	}
}

// Subscribe registers a new subscriber.
func (gb *GlobalBus) Subscribe(subscriberID string) chan AgentEvent {
	gb.mu.Lock()
	defer gb.mu.Unlock()

	ch := make(chan AgentEvent, globalBusChannelBuffer)
	gb.subscribers[subscriberID] = ch
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (gb *GlobalBus) Unsubscribe(subscriberID string) {
	gb.mu.Lock()
	defer gb.mu.Unlock()

	if ch, ok := gb.subscribers[subscriberID]; ok {
		close(ch)
		delete(gb.subscribers, subscriberID)
	}
}

// SubscriberCount returns the number of active subscribers.
func (gb *GlobalBus) SubscriberCount() int {
	gb.mu.RLock()
	defer gb.mu.RUnlock()
	return len(gb.subscribers)
}

// ---- EventHub ----

// EventHub is the central event coordinator held by Manager.
// It manages one GlobalBus (for agent-level events) and
// per-session SessionBuses (for streaming events).
type EventHub struct {
	mu           sync.RWMutex
	globalBus    *GlobalBus
	sessionBuses map[string]*SessionBus // sessionID -> bus
}

// NewEventHub creates a new EventHub.
func NewEventHub() *EventHub {
	return &EventHub{
		globalBus:    NewGlobalBus(),
		sessionBuses: make(map[string]*SessionBus),
	}
}

// Global returns the GlobalBus.
func (eh *EventHub) Global() *GlobalBus {
	return eh.globalBus
}

// GetOrCreateSessionBus returns (or creates) a SessionBus for the given session.
func (eh *EventHub) GetOrCreateSessionBus(sessionID string) *SessionBus {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	if bus, ok := eh.sessionBuses[sessionID]; ok {
		return bus
	}
	bus := NewSessionBus(sessionID)
	eh.sessionBuses[sessionID] = bus
	return bus
}

// GetSessionBus returns the SessionBus for a session, or nil if none exists.
func (eh *EventHub) GetSessionBus(sessionID string) *SessionBus {
	eh.mu.RLock()
	defer eh.mu.RUnlock()
	return eh.sessionBuses[sessionID]
}

// RemoveSessionBus removes and returns the SessionBus for a session.
func (eh *EventHub) RemoveSessionBus(sessionID string) {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	delete(eh.sessionBuses, sessionID)
}
