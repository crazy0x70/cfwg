package runtime

import (
	"sync"
	"time"
)

type Snapshot struct {
	Ready     bool      `json:"ready"`
	StartedAt time.Time `json:"startedAt"`
}

type Status struct {
	mu        sync.RWMutex
	ready     bool
	startedAt time.Time
}

func NewStatus(startedAt time.Time) *Status {
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	return &Status{
		startedAt: startedAt,
	}
}

func (s *Status) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ready = ready
}

func (s *Status) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.ready
}

func (s *Status) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Snapshot{
		Ready:     s.ready,
		StartedAt: s.startedAt,
	}
}
