package job

import "time"

type Status string

const (
	StatusProcessing Status = "processing"
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
)

type Job struct {
	ID           string
	Operation    string
	Params       map[string]any
	Status       Status
	OutputPath   string
	OutputPaths  []string
	ErrorCode    string
	ErrorMessage string
	RetryCount   int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
