package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type SynthesizeRequest struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
	Voice  string `json:"voice"`
	Rate   string `json:"rate"`
	Volume string `json:"volume"`
	Pitch  string `json:"pitch"`
	Format string `json:"format"`
}

type SynthesizeResult struct {
	TaskID         string  `json:"task_id"`
	AudioLocalPath string  `json:"audio_local_path"`
	URL            string  `json:"url"`
	DurationSec    float64 `json:"duration_sec"`
}

type errorResponse struct {
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, timeoutMs int) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
		},
	}
}

func (c *Client) Synthesize(ctx context.Context, req SynthesizeRequest) (*SynthesizeResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/synthesize", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("worker http error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp errorResponse
		if jsonErr := json.NewDecoder(resp.Body).Decode(&errResp); jsonErr != nil || errResp.ErrorCode == "" {
			errResp.ErrorCode = "worker_failed"
			errResp.ErrorMessage = fmt.Sprintf("worker returned status %d", resp.StatusCode)
		}
		return nil, &WorkerError{Code: errResp.ErrorCode, Message: errResp.ErrorMessage}
	}

	var result SynthesizeResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode worker response: %w", err)
	}
	return &result, nil
}

type WorkerError struct {
	Code    string
	Message string
}

func (e *WorkerError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
