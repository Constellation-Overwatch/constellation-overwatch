package timeseries

import (
	"sort"
	"sync"
	"time"
)

// DefaultCapacity is the default ring buffer size (60 samples = 1 minute at 1s intervals).
const DefaultCapacity = 60

// Store manages a collection of named time-series ring buffers.
type Store struct {
	mu       sync.RWMutex
	series   map[string]*RingBuffer
	capacity int
}

// NewStore creates a new time-series store with the given buffer capacity.
func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Store{
		series:   make(map[string]*RingBuffer),
		capacity: capacity,
	}
}

// Record adds a value to the named series, creating it if it doesn't exist.
func (s *Store) Record(name string, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rb, ok := s.series[name]
	if !ok {
		rb = NewRingBuffer(s.capacity)
		s.series[name] = rb
	}
	rb.Push(value, time.Now().UnixMilli())
}

// Get returns the ring buffer for a named series, or nil if not found.
func (s *Store) Get(name string) *RingBuffer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.series[name]
}

// Names returns all series names in sorted order.
func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.series))
	for name := range s.series {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
