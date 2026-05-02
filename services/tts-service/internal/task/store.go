package task

import (
	"fmt"
	"sync"
	"time"
)

// Store 任务状态存储接口，MVP 内存实现，生产可替换为 RedisStore。
type Store interface {
	Save(t *Task) error
	Get(id string) (*Task, error)
	MarkDone(id, audioLocalPath, url string, durationSec float64) error
	MarkFailed(id, errorCode, errorMessage string) error
	IncrRetry(id string) error
}

// MemoryStore 基于内存的 Store 实现，进程重启后数据丢失。
type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tasks: make(map[string]*Task)}
}

func (s *MemoryStore) Save(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
	return nil
}

func (s *MemoryStore) Get(id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return t, nil
}

func (s *MemoryStore) MarkDone(id, audioLocalPath, url string, durationSec float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	t.Status = StatusDone
	t.AudioLocalPath = audioLocalPath
	t.URL = url
	t.DurationSec = durationSec
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) MarkFailed(id, errorCode, errorMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	t.Status = StatusFailed
	t.ErrorCode = errorCode
	t.ErrorMessage = errorMessage
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) IncrRetry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.RetryCount++
	}
	return nil
}
