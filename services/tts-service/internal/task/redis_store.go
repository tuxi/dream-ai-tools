package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	taskKeyPrefix = "tts:task:"
	taskTTL       = 24 * time.Hour
)

// RedisStore 基于 Redis 的 Store 实现，支持多实例和重启恢复。
type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) key(id string) string {
	return taskKeyPrefix + id
}

func (s *RedisStore) Save(t *Task) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.client.Set(context.Background(), s.key(t.ID), data, taskTTL).Err()
}

func (s *RedisStore) Get(id string) (*Task, error) {
	data, err := s.client.Get(context.Background(), s.key(id)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *RedisStore) MarkDone(id, audioLocalPath, url string, durationSec float64) error {
	return s.update(id, func(t *Task) {
		t.Status = StatusDone
		t.AudioLocalPath = audioLocalPath
		t.URL = url
		t.DurationSec = durationSec
		t.UpdatedAt = time.Now()
	})
}

func (s *RedisStore) MarkFailed(id, errorCode, errorMessage string) error {
	return s.update(id, func(t *Task) {
		t.Status = StatusFailed
		t.ErrorCode = errorCode
		t.ErrorMessage = errorMessage
		t.UpdatedAt = time.Now()
	})
}

func (s *RedisStore) IncrRetry(id string) error {
	return s.update(id, func(t *Task) {
		t.RetryCount++
	})
}

// update 读取 → 修改 → 写回，使用乐观锁重试保证并发安全。
func (s *RedisStore) update(id string, fn func(*Task)) error {
	ctx := context.Background()
	key := s.key(id)

	for range 3 {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			data, err := tx.Get(ctx, key).Bytes()
			if err == redis.Nil {
				return fmt.Errorf("task not found: %s", id)
			}
			if err != nil {
				return err
			}
			var t Task
			if err := json.Unmarshal(data, &t); err != nil {
				return err
			}
			fn(&t)
			newData, err := json.Marshal(&t)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, newData, taskTTL)
				return nil
			})
			return err
		}, key)

		if err == nil {
			return nil
		}
		if err != redis.TxFailedErr {
			return err
		}
	}
	return fmt.Errorf("redis update conflict, task_id=%s", id)
}
