// 复制到主项目: ai-engine/pkg/ffmpeg/providers/ffmpeg_service_provider.go
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// FFmpegServiceProvider 通过 HTTP 调用独立的 FFmpeg Service，
// 替代主系统中各工具直接 exec.Command("ffmpeg") 的调用。
// 核心 API：SubmitJob（异步提交）+ WaitJob（轮询等待）+ Probe（同步探测）。
type FFmpegServiceProvider struct {
	baseURL      string
	submitClient *http.Client
	pollClient   *http.Client
	pollInterval time.Duration
	waitTimeout  time.Duration
}

// FFmpegServiceConfig 是构造 FFmpegServiceProvider 所需的配置。
type FFmpegServiceConfig struct {
	ServiceURL     string
	SubmitTimeoutMs int
	WaitTimeoutMs   int
	PollIntervalMs  int
}

func NewFFmpegServiceProvider(cfg FFmpegServiceConfig) *FFmpegServiceProvider {
	if cfg.SubmitTimeoutMs <= 0 {
		cfg.SubmitTimeoutMs = 1000
	}
	if cfg.WaitTimeoutMs <= 0 {
		cfg.WaitTimeoutMs = 300000
	}
	if cfg.PollIntervalMs <= 0 {
		cfg.PollIntervalMs = 2000
	}
	return &FFmpegServiceProvider{
		baseURL:      cfg.ServiceURL,
		submitClient: &http.Client{Timeout: time.Duration(cfg.SubmitTimeoutMs) * time.Millisecond},
		pollClient:   &http.Client{Timeout: 10 * time.Second},
		pollInterval: time.Duration(cfg.PollIntervalMs) * time.Millisecond,
		waitTimeout:  time.Duration(cfg.WaitTimeoutMs) * time.Millisecond,
	}
}

// ─────────────────────────────────────────────
// 核心方法：SubmitJob / WaitJob
// ─────────────────────────────────────────────

type submitJobRequest struct {
	Operation string         `json:"operation"`
	Params    map[string]any `json:"params"`
}

type submitJobResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// SubmitJob 向 FFmpeg Service 提交一个异步任务，立即返回 job_id。
// operation 为操作名（如 "mix-audio"），params 为该操作的参数 map。
func (p *FFmpegServiceProvider) SubmitJob(ctx context.Context, operation string, params map[string]any) (string, error) {
	if params == nil {
		params = map[string]any{}
	}

	body, err := json.Marshal(submitJobRequest{Operation: operation, Params: params})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/v1/ffmpeg/jobs", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.submitClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ffmpeg-service submit failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ffmpeg-service submit returned status %d", resp.StatusCode)
	}

	var result submitJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}
	return result.JobID, nil
}

type jobResultResponse struct {
	JobID        string   `json:"job_id"`
	Status       string   `json:"status"`
	OutputPath   string   `json:"output_path"`
	OutputPaths  []string `json:"output_paths"`
	ErrorCode    string   `json:"error_code"`
	ErrorMessage string   `json:"error_message"`
}

// WaitJob 轮询任务状态直到 done/failed，或 ctx 超时。
// 返回 outputPath（单文件）和 outputPaths（多文件，如帧序列）。
func (p *FFmpegServiceProvider) WaitJob(ctx context.Context, jobID string) (outputPath string, outputPaths []string, err error) {
	deadline := time.Now().Add(p.waitTimeout)
	pollURL := fmt.Sprintf("%s/api/v1/ffmpeg/jobs/result?id=%s", p.baseURL, jobID)

	for {
		if time.Now().After(deadline) {
			return "", nil, fmt.Errorf("ffmpeg-service wait timeout after %s for job %s", p.waitTimeout, jobID)
		}

		result, err := p.pollOnce(ctx, pollURL)
		if err != nil {
			return "", nil, err
		}

		switch result.Status {
		case "done":
			return result.OutputPath, result.OutputPaths, nil
		case "failed":
			return "", nil, fmt.Errorf("ffmpeg job failed job_id=%s error_code=%s: %s",
				jobID, result.ErrorCode, result.ErrorMessage)
		case "processing":
			// continue polling
		default:
			return "", nil, fmt.Errorf("ffmpeg-service unexpected status: %s", result.Status)
		}

		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(p.pollInterval):
		}
	}
}

func (p *FFmpegServiceProvider) pollOnce(ctx context.Context, url string) (*jobResultResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.pollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg-service poll failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ffmpeg-service poll returned status %d", resp.StatusCode)
	}

	var result jobResultResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}
	return &result, nil
}

// ─────────────────────────────────────────────
// probe：同步接口
// ─────────────────────────────────────────────

type ProbeRequest struct {
	Path string `json:"path"`
}

// ProbeInfo 是 POST /api/v1/ffmpeg/probe 的响应结构。
type ProbeInfo struct {
	DurationSec float64      `json:"duration_sec"`
	Width       int          `json:"width"`
	Height      int          `json:"height"`
	SizeBytes   int64        `json:"size_bytes"`
	Streams     []StreamInfo `json:"streams"`
}

type StreamInfo struct {
	CodecType  string  `json:"codec_type"`
	CodecName  string  `json:"codec_name"`
	FPS        float64 `json:"fps,omitempty"`
	SampleRate int     `json:"sample_rate,omitempty"`
}

// Probe 同步调用 ffprobe，返回媒体文件元信息。
func (p *FFmpegServiceProvider) Probe(ctx context.Context, path string) (*ProbeInfo, error) {
	body, err := json.Marshal(ProbeRequest{Path: path})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/v1/ffmpeg/probe", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.submitClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg-service probe failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ffmpeg-service probe returned status %d", resp.StatusCode)
	}

	var info ProbeInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode probe response: %w", err)
	}
	return &info, nil
}

// ─────────────────────────────────────────────
// 各操作参数结构体 + 类型化提交方法
// 调用方可直接用类型化方法，也可直接调 SubmitJob。
// ─────────────────────────────────────────────

// MixAudioParams 对应 ffmpeg-service "mix-audio" 操作。
type MixAudioParams struct {
	TTSPath      string  `json:"tts_path"`
	BGMPath      string  `json:"bgm_path"`
	TTSVolume    float64 `json:"tts_volume"`
	BGMVolume    float64 `json:"bgm_volume"`
	DurationSec  float64 `json:"duration_sec"`
	OutputFormat string  `json:"output_format"`
}

func (p *FFmpegServiceProvider) SubmitMixAudio(ctx context.Context, params MixAudioParams) (string, error) {
	return p.SubmitJob(ctx, "mix-audio", toParamMap(params))
}

// ConcatAudioParams 对应 "concat-audio" 操作。
type ConcatAudioParams struct {
	AudioPaths   []string `json:"audio_paths"`
	GapSec       float64  `json:"gap_sec"`
	OutputFormat string   `json:"output_format"`
}

func (p *FFmpegServiceProvider) SubmitConcatAudio(ctx context.Context, params ConcatAudioParams) (string, error) {
	return p.SubmitJob(ctx, "concat-audio", toParamMap(params))
}

// ConcatVideoParams 对应 "concat-video" 操作。
type ConcatVideoParams struct {
	VideoPaths []string `json:"video_paths"`
	Reencode   bool     `json:"reencode"`
}

func (p *FFmpegServiceProvider) SubmitConcatVideo(ctx context.Context, params ConcatVideoParams) (string, error) {
	return p.SubmitJob(ctx, "concat-video", toParamMap(params))
}

// FramesToVideoParams 对应 "frames-to-video" 操作。
type FramesToVideoParams struct {
	FrameDir     string `json:"frame_dir"`
	FramePattern string `json:"frame_pattern"`
	FPS          int    `json:"fps"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	OutputFormat string `json:"output_format"`
}

func (p *FFmpegServiceProvider) SubmitFramesToVideo(ctx context.Context, params FramesToVideoParams) (string, error) {
	return p.SubmitJob(ctx, "frames-to-video", toParamMap(params))
}

// ImageToFramesParams 对应 "image-to-frames" 操作。
type ImageToFramesParams struct {
	ImagePath   string  `json:"image_path"`
	FPS         int     `json:"fps"`
	DurationSec float64 `json:"duration_sec"`
	OutputDir   string  `json:"output_dir"`
}

func (p *FFmpegServiceProvider) SubmitImageToFrames(ctx context.Context, params ImageToFramesParams) (string, error) {
	return p.SubmitJob(ctx, "image-to-frames", toParamMap(params))
}

// MergeAVParams 对应 "merge-av" 操作。
type MergeAVParams struct {
	VideoPath string `json:"video_path"`
	AudioPath string `json:"audio_path"`
	Shortest  bool   `json:"shortest"`
}

func (p *FFmpegServiceProvider) SubmitMergeAV(ctx context.Context, params MergeAVParams) (string, error) {
	return p.SubmitJob(ctx, "merge-av", toParamMap(params))
}

// BurnSubtitleParams 对应 "burn-subtitle" 操作。
type BurnSubtitleParams struct {
	VideoPath     string `json:"video_path"`
	SubtitlePath  string `json:"subtitle_path"`
	StyleOverride string `json:"style_override"`
}

func (p *FFmpegServiceProvider) SubmitBurnSubtitle(ctx context.Context, params BurnSubtitleParams) (string, error) {
	return p.SubmitJob(ctx, "burn-subtitle", toParamMap(params))
}

// ExtractFrameParams 对应 "extract-frame" 操作。
// Position: "head" | "tail" | "time"；position="time" 时 TimeSec 必填。
type ExtractFrameParams struct {
	VideoPath    string  `json:"video_path"`
	Position     string  `json:"position"`
	TimeSec      float64 `json:"time_sec,omitempty"`
	OutputFormat string  `json:"output_format"`
}

func (p *FFmpegServiceProvider) SubmitExtractFrame(ctx context.Context, params ExtractFrameParams) (string, error) {
	return p.SubmitJob(ctx, "extract-frame", toParamMap(params))
}

// PostprocessParams 对应 "postprocess" 操作。
type PostprocessParams struct {
	VideoPath           string         `json:"video_path"`
	WatermarkText       string         `json:"watermark_text,omitempty"`
	WatermarkImagePath  string         `json:"watermark_image_path,omitempty"`
	DrawtextParams      map[string]any `json:"drawtext_params,omitempty"`
}

func (p *FFmpegServiceProvider) SubmitPostprocess(ctx context.Context, params PostprocessParams) (string, error) {
	return p.SubmitJob(ctx, "postprocess", toParamMap(params))
}

// ImagePreprocessParams 对应 "image-preprocess" 操作。
// FitMode: "cover" | "contain" | "fill"
type ImagePreprocessParams struct {
	ImagePath     string `json:"image_path"`
	TargetWidth   int    `json:"target_width"`
	TargetHeight  int    `json:"target_height"`
	FitMode       string `json:"fit_mode"`
	OutputFormat  string `json:"output_format"`
}

func (p *FFmpegServiceProvider) SubmitImagePreprocess(ctx context.Context, params ImagePreprocessParams) (string, error) {
	return p.SubmitJob(ctx, "image-preprocess", toParamMap(params))
}

// ─────────────────────────────────────────────
// 内部工具
// ─────────────────────────────────────────────

// toParamMap 将任意结构体序列化为 map[string]any，通过 JSON 中转。
func toParamMap(v any) map[string]any {
	data, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}
