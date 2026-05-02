package task

import (
	"fmt"
	"sync"
	"time"
)

type Store struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

func NewStore() *Store {
	return &Store{tasks: make(map[string]*Task)}
}

func (s *Store) Save(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
}

func (s *Store) Get(id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return t, nil
}

func (s *Store) MarkDone(id, audioLocalPath, url string, durationSec float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = StatusDone
		t.AudioLocalPath = audioLocalPath
		t.URL = url
		t.DurationSec = durationSec
		t.UpdatedAt = time.Now()
	}
}

func (s *Store) MarkFailed(id, errorCode, errorMessage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = StatusFailed
		t.ErrorCode = errorCode
		t.ErrorMessage = errorMessage
		t.UpdatedAt = time.Now()
	}
}

func (s *Store) IncrRetry(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.RetryCount++
	}
}
