// 复制到主项目: ai-engine/pkg/tts/providers/edge_service_provider.go
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	aitts "github.com/tuxi/dream-ai/ai-engine/pkg/tts"
)

// EdgeServiceProvider 通过 HTTP 调用独立的 Gin TTS Service，
// 替代原 EdgeProvider 的本地 edge-tts CLI 调用。
// 实现 tts.AsyncProvider 接口。
type EdgeServiceProvider struct {
	baseURL      string
	submitClient *http.Client // 用于提交任务，超时短
	pollClient   *http.Client // 用于轮询结果，超时长
	pollInterval time.Duration
	waitTimeout  time.Duration
}

type EdgeServiceConfig struct {
	ServiceURL     string
	SubmitTimeoutMs int
	WaitTimeoutMs   int
	PollIntervalMs  int
}

func NewEdgeServiceProvider(cfg EdgeServiceConfig) *EdgeServiceProvider {
	if cfg.SubmitTimeoutMs <= 0 {
		cfg.SubmitTimeoutMs = 1000
	}
	if cfg.WaitTimeoutMs <= 0 {
		cfg.WaitTimeoutMs = 90000
	}
	if cfg.PollIntervalMs <= 0 {
		cfg.PollIntervalMs = 1000
	}
	return &EdgeServiceProvider{
		baseURL:      cfg.ServiceURL,
		submitClient: &http.Client{Timeout: time.Duration(cfg.SubmitTimeoutMs) * time.Millisecond},
		pollClient:   &http.Client{Timeout: 10 * time.Second},
		pollInterval: time.Duration(cfg.PollIntervalMs) * time.Millisecond,
		waitTimeout:  time.Duration(cfg.WaitTimeoutMs) * time.Millisecond,
	}
}

func (p *EdgeServiceProvider) Name() aitts.ProviderName {
	return aitts.ProviderEdge
}

// Synthesize 同步调用：内部串联 SubmitSynthesize + WaitSynthesize。
func (p *EdgeServiceProvider) Synthesize(ctx context.Context, req aitts.SynthesizeRequest) (*aitts.SynthesizeResult, error) {
	submitReq := aitts.SubmitSynthesizeRequest{SynthesizeRequest: req}
	submitResult, err := p.SubmitSynthesize(ctx, submitReq)
	if err != nil {
		return nil, err
	}
	waitReq := aitts.WaitSynthesizeRequest{
		Provider:     aitts.ProviderEdge,
		Protocol:     aitts.TransportProtocolAsync,
		JobID:        submitResult.JobID,
		WorkflowName: req.WorkflowName,
		TaskID:       req.TaskID,
		WaitTimeout:  p.waitTimeout,
		PollInterval: p.pollInterval,
	}
	return p.WaitSynthesize(ctx, waitReq)
}

// ─────────────────────────────────────────────
// 提交：POST /api/v1/tts
// ─────────────────────────────────────────────

type createTaskRequest struct {
	Text   string `json:"text"`
	Voice  string `json:"voice"`
	Rate   string `json:"rate"`
	Volume string `json:"volume"`
	Pitch  string `json:"pitch"`
	Format string `json:"format"`
}

type createTaskResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

func (p *EdgeServiceProvider) SubmitSynthesize(ctx context.Context, req aitts.SubmitSynthesizeRequest) (*aitts.SubmitSynthesizeResult, error) {
	start := time.Now()

	body, _ := json.Marshal(createTaskRequest{
		Text:   req.Text,
		Voice:  req.Voice,
		Rate:   req.Rate,
		Volume: req.Volume,
		Pitch:  req.Pitch,
		Format: req.Format,
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/v1/tts", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.submitClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tts-service submit failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts-service submit returned status %d", resp.StatusCode)
	}

	var result createTaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode submit response: %w", err)
	}

	return &aitts.SubmitSynthesizeResult{
		Provider:   aitts.ProviderEdge,
		Protocol:   aitts.TransportProtocolAsync,
		Status:     aitts.SubmissionStatusAccepted,
		JobID:      result.TaskID,
		AcceptedAt: start,
	}, nil
}

// ─────────────────────────────────────────────
// 轮询：GET /api/v1/tts/result?id=...
// ─────────────────────────────────────────────

type taskResultResponse struct {
	TaskID         string  `json:"task_id"`
	Status         string  `json:"status"`
	URL            string  `json:"url"`
	AudioLocalPath string  `json:"audio_local_path"`
	DurationSec    float64 `json:"duration_sec"`
	ErrorCode      string  `json:"error_code"`
	ErrorMessage   string  `json:"error_message"`
}

func (p *EdgeServiceProvider) WaitSynthesize(ctx context.Context, req aitts.WaitSynthesizeRequest) (*aitts.SynthesizeResult, error) {
	waitTimeout := req.WaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = p.waitTimeout
	}
	pollInterval := req.PollInterval
	if pollInterval <= 0 {
		pollInterval = p.pollInterval
	}

	deadline := time.Now().Add(waitTimeout)
	url := fmt.Sprintf("%s/api/v1/tts/result?id=%s", p.baseURL, req.JobID)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait timeout after %s for job %s", waitTimeout, req.JobID)
		}

		result, err := p.poll(ctx, url)
		if err != nil {
			return nil, err
		}

		switch result.Status {
		case "done":
			return &aitts.SynthesizeResult{
				AudioLocalPath:   result.AudioLocalPath,
				DurationSec:      result.DurationSec,
				Provider:         aitts.ProviderEdge,
				Protocol:         aitts.TransportProtocolAsync,
				SubmissionStatus: aitts.SubmissionStatusCompleted,
				JobID:            req.JobID,
			}, nil

		case "failed":
			return nil, fmt.Errorf("tts task failed job_id=%s error_code=%s: %s",
				req.JobID, result.ErrorCode, result.ErrorMessage)

		case "processing":
			// 继续轮询

		default:
			return nil, fmt.Errorf("unexpected task status: %s", result.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (p *EdgeServiceProvider) poll(ctx context.Context, url string) (*taskResultResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.pollClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tts-service poll failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts-service poll returned status %d", resp.StatusCode)
	}

	var result taskResultResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}
	return &result, nil
}
