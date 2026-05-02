package task

import "time"

type Status string

const (
	StatusProcessing Status = "processing"
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
)

type Task struct {
	ID             string
	Text           string
	Voice          string
	Rate           string
	Volume         string
	Pitch          string
	Format         string
	Status         Status
	URL            string
	AudioLocalPath string
	DurationSec    float64
	ErrorCode      string
	ErrorMessage   string
	RetryCount     int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
