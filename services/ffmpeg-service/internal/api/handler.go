package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/tuxi/dream-ai-tools/ffmpeg-service/internal/executor"
	"github.com/tuxi/dream-ai-tools/ffmpeg-service/internal/job"
)

// Config holds handler-level configuration.
type Config struct {
	RetryTimes int
	TimeoutMs  int
	ExecConfig executor.Config
}

// Handler implements the HTTP API for ffmpeg-service.
type Handler struct {
	store job.Store
	cfg   Config
}

func NewHandler(store job.Store, cfg Config) *Handler {
	if cfg.RetryTimes < 0 {
		cfg.RetryTimes = 0
	}
	return &Handler{store: store, cfg: cfg}
}

// submitRequest is the body of POST /api/v1/ffmpeg/jobs.
type submitRequest struct {
	Operation string         `json:"operation"`
	Params    map[string]any `json:"params"`
}

// Submit creates a new async ffmpeg job and dispatches it to a goroutine.
func (h *Handler) Submit(c *gin.Context) {
	var req submitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": err.Error()})
		return
	}

	req.Operation = strings.TrimSpace(req.Operation)
	if req.Operation == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "operation is required"})
		return
	}

	if req.Params == nil {
		req.Params = map[string]any{}
	}

	jobID := "ffmpeg_" + ulid.Make().String()
	j := &job.Job{
		ID:        jobID,
		Operation: req.Operation,
		Params:    req.Params,
		Status:    job.StatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.store.Save(j); err != nil {
		slog.Error("save job failed", "job_id", jobID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "store_error", "error_message": err.Error()})
		return
	}

	go h.dispatch(jobID, req.Operation, req.Params)

	c.JSON(http.StatusOK, gin.H{
		"job_id": jobID,
		"status": string(job.StatusProcessing),
	})
}

// dispatch runs the executor in a goroutine with retry logic.
func (h *Handler) dispatch(jobID, operation string, params map[string]any) {
	j := &job.Job{
		ID:        jobID,
		Operation: operation,
		Params:    params,
	}

	var (
		outputPath  string
		outputPaths []string
		lastErr     error
	)

	for attempt := 0; attempt <= h.cfg.RetryTimes; attempt++ {
		if attempt > 0 {
			h.store.IncrRetry(jobID)
			slog.Info("retrying job", "job_id", jobID, "operation", operation, "attempt", attempt)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.cfg.TimeoutMs)*time.Millisecond)
		outputPath, outputPaths, lastErr = executor.Dispatch(ctx, j, h.cfg.ExecConfig)
		cancel()

		if lastErr == nil {
			break
		}
		slog.Warn("job attempt failed", "job_id", jobID, "operation", operation, "attempt", attempt, "error", lastErr)
	}

	if lastErr != nil {
		slog.Error("job failed", "job_id", jobID, "operation", operation, "status", "failed", "error", lastErr)
		h.store.MarkFailed(jobID, "ffmpeg_failed", lastErr.Error())
		return
	}

	slog.Info("job done", "job_id", jobID, "operation", operation, "status", "done",
		"output_path", outputPath)
	h.store.MarkDone(jobID, outputPath, outputPaths)
}

// Result handles GET /api/v1/ffmpeg/jobs/result?id={job_id}.
func (h *Handler) Result(c *gin.Context) {
	id := strings.TrimSpace(c.Query("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "id is required"})
		return
	}

	j, err := h.store.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_code": "not_found", "error_message": "job not found"})
		return
	}

	resp := gin.H{
		"job_id": j.ID,
		"status": string(j.Status),
	}
	if j.Status == job.StatusDone {
		resp["output_path"] = j.OutputPath
		if len(j.OutputPaths) > 0 {
			resp["output_paths"] = j.OutputPaths
		}
	}
	if j.Status == job.StatusFailed {
		resp["error_code"] = j.ErrorCode
		resp["error_message"] = j.ErrorMessage
	}

	c.JSON(http.StatusOK, resp)
}

// ProbeRequest is the body of POST /api/v1/ffmpeg/probe.
type ProbeRequest struct {
	Path string `json:"path"`
}

// ProbeHandler handles the synchronous probe endpoint.
func (h *Handler) ProbeHandler(c *gin.Context) {
	var req ProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": err.Error()})
		return
	}

	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "path is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	info, err := executor.Probe(ctx, h.cfg.ExecConfig.FFprobePath, req.Path)
	if err != nil {
		slog.Error("probe failed", "path", req.Path, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "probe_failed", "error_message": err.Error()})
		return
	}

	slog.Info("probe done",
		"path", req.Path,
		"duration_sec", info.DurationSec,
		"width", info.Width,
		"height", info.Height,
		"size_bytes", info.SizeBytes,
		"stream_count", len(info.Streams),
	)

	c.JSON(http.StatusOK, info)
}
