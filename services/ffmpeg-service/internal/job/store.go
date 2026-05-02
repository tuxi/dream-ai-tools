package job

import (
	"fmt"
	"sync"
	"time"
)

// Store is the job state storage interface. MVP uses MemoryStore; production uses RedisStore.
type Store interface {
	Save(j *Job) error
	Get(id string) (*Job, error)
	MarkDone(id, outputPath string, outputPaths []string) error
	MarkFailed(id, errorCode, errorMessage string) error
	IncrRetry(id string) error
}

// MemoryStore is an in-process store; data is lost on restart.
type MemoryStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{jobs: make(map[string]*Job)}
}

func (s *MemoryStore) Save(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	return nil
}

func (s *MemoryStore) Get(id string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	return j, nil
}

func (s *MemoryStore) MarkDone(id, outputPath string, outputPaths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}
	j.Status = StatusDone
	j.OutputPath = outputPath
	j.OutputPaths = outputPaths
	j.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) MarkFailed(id, errorCode, errorMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}
	j.Status = StatusFailed
	j.ErrorCode = errorCode
	j.ErrorMessage = errorMessage
	j.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) IncrRetry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.RetryCount++
	}
	return nil
}
