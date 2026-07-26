// Package embeddings provides codebase indexing with embedding vectors and progress tracking.
package embeddings

import (
	"sync"
	"time"
)

// IndexStatus represents the current state of the codebase index.
type IndexStatus string

const (
	IndexStatusDisabled IndexStatus = "disabled"
	IndexStatusIdle     IndexStatus = "idle"
	IndexStatusIndexing IndexStatus = "indexing"
	IndexStatusComplete IndexStatus = "complete"
	IndexStatusError    IndexStatus = "error"
)

// IndexState tracks the progress and status of codebase indexing.
type IndexState struct {
	mu            sync.RWMutex
	Status        IndexStatus
	ChunksIndexed int
	TotalChunks   int
	UpdatedAt     time.Time
	Error         string
}

// NewIndexState creates a new IndexState in the idle status.
func NewIndexState() *IndexState {
	return &IndexState{
		Status:    IndexStatusIdle,
		UpdatedAt: time.Now(),
	}
}

// Update updates the index state with new progress.
func (s *IndexState) Update(status IndexStatus, chunksIndexed, totalChunks int, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
	s.ChunksIndexed = chunksIndexed
	s.TotalChunks = totalChunks
	s.UpdatedAt = time.Now()
	s.Error = errMsg
}

// Get returns a copy of the current state (thread-safe).
func (s *IndexState) Get() (IndexStatus, int, int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status, s.ChunksIndexed, s.TotalChunks, s.Error
}

// Percent returns the indexing progress percentage, or -1 if total is 0.
func (s *IndexState) Percent() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.TotalChunks == 0 {
		return -1
	}
	return s.ChunksIndexed * 100 / s.TotalChunks
}
