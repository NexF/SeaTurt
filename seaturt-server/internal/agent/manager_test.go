package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// newTestManager creates a Manager with just the session/tool call maps initialized (no store/docker).
func newTestManager() *Manager {
	return &Manager{
		activeSessions:  make(map[string]context.CancelFunc),
		activeToolCalls: make(map[string]map[string]context.CancelFunc),
	}
}

func TestSetAndCancelActiveSession(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	ctx, cancel := context.WithCancel(context.Background())
	m.SetActiveSession("agent1", cancel)

	// Cancel should return true and cancel the context
	assert.True(t, m.CancelActiveSession("agent1"))
	assert.Error(t, ctx.Err()) // context should be cancelled

	// Cancel again should return false
	assert.False(t, m.CancelActiveSession("agent1"))
}

func TestSetActiveSession_ReplacesExisting(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	ctx1, cancel1 := context.WithCancel(context.Background())
	m.SetActiveSession("agent1", cancel1)

	_, cancel2 := context.WithCancel(context.Background())
	m.SetActiveSession("agent1", cancel2)

	// First context should have been cancelled by SetActiveSession
	assert.Error(t, ctx1.Err())
}

func TestClearActiveSession(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	_, cancel := context.WithCancel(context.Background())
	m.SetActiveSession("agent1", cancel)
	m.ClearActiveSession("agent1")

	// Cancel should return false (cleared)
	assert.False(t, m.CancelActiveSession("agent1"))
}

func TestSetAndCancelActiveToolCall(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	ctx, cancel := context.WithCancel(context.Background())
	m.SetActiveToolCall("agent1", "tc_1", cancel)

	assert.True(t, m.CancelActiveToolCall("agent1", "tc_1"))
	assert.Error(t, ctx.Err())

	// Cancel again should return false
	assert.False(t, m.CancelActiveToolCall("agent1", "tc_1"))
}

func TestCancelActiveSession_AlsoClearsToolCalls(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	_, sessionCancel := context.WithCancel(context.Background())
	m.SetActiveSession("agent1", sessionCancel)

	_, toolCancel := context.WithCancel(context.Background())
	m.SetActiveToolCall("agent1", "tc_1", toolCancel)

	m.CancelActiveSession("agent1")

	// Tool call should also be cleared
	assert.False(t, m.CancelActiveToolCall("agent1", "tc_1"))
}

func TestConcurrentSessionAccess(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cancel := context.WithCancel(context.Background())
			m.SetActiveSession("agent1", cancel)
			m.CancelActiveSession("agent1")
		}()
	}
	wg.Wait()
}

func TestConcurrentToolCallAccess(t *testing.T) {
	t.Parallel()
	m := newTestManager()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			toolCallID := "tc_" + string(rune('0'+id%10))
			_, cancel := context.WithCancel(context.Background())
			m.SetActiveToolCall("agent1", toolCallID, cancel)
			m.ClearActiveToolCall("agent1", toolCallID)
		}(i)
	}
	wg.Wait()
}
