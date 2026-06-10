package job

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	jobKeyPrefix = "ffmpeg:job:"
	jobTTL       = 24 * time.Hour
)

// RedisStore persists jobs in Redis; supports multi-instance and restart recovery.
type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) key(id string) string {
	return jobKeyPrefix + id
}

func (s *RedisStore) Save(j *Job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return s.client.Set(context.Background(), s.key(j.ID), data, jobTTL).Err()
}

func (s *RedisStore) Get(id string) (*Job, error) {
	data, err := s.client.Get(context.Background(), s.key(id)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *RedisStore) MarkDone(id, outputPath string, outputPaths []string, outputData map[string]any) error {
	return s.update(id, func(j *Job) {
		j.Status = StatusDone
		j.OutputPath = outputPath
		j.OutputPaths = outputPaths
		j.OutputData = outputData
		j.UpdatedAt = time.Now()
	})
}

func (s *RedisStore) MarkFailed(id, errorCode, errorMessage string) error {
	return s.update(id, func(j *Job) {
		j.Status = StatusFailed
		j.ErrorCode = errorCode
		j.ErrorMessage = errorMessage
		j.UpdatedAt = time.Now()
	})
}

func (s *RedisStore) IncrRetry(id string) error {
	return s.update(id, func(j *Job) {
		j.RetryCount++
	})
}

func (s *RedisStore) update(id string, fn func(*Job)) error {
	ctx := context.Background()
	key := s.key(id)

	for range 3 {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			data, err := tx.Get(ctx, key).Bytes()
			if err == redis.Nil {
				return fmt.Errorf("job not found: %s", id)
			}
			if err != nil {
				return err
			}
			var j Job
			if err := json.Unmarshal(data, &j); err != nil {
				return err
			}
			fn(&j)
			newData, err := json.Marshal(&j)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, newData, jobTTL)
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
	return fmt.Errorf("redis update conflict, job_id=%s", id)
}
