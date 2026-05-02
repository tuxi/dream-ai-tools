package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/tuxi/dream-ai-tools/tts-service/internal/task"
	"github.com/tuxi/dream-ai-tools/tts-service/internal/worker"
)

type Config struct {
	WorkerRetryTimes int
	WorkerTimeoutMs  int
}

type Handler struct {
	store      task.Store
	worker     *worker.Client
	retrytimes int
	workerMs   int
}

func NewHandler(store task.Store, workerClient *worker.Client, cfg Config) *Handler {
	retries := cfg.WorkerRetryTimes
	if retries < 0 {
		retries = 0
	}
	return &Handler{
		store:      store,
		worker:     workerClient,
		retrytimes: retries,
		workerMs:   cfg.WorkerTimeoutMs,
	}
}

type createRequest struct {
	Text   string `json:"text"`
	Voice  string `json:"voice"`
	Rate   string `json:"rate"`
	Volume string `json:"volume"`
	Pitch  string `json:"pitch"`
	Format string `json:"format"`
}

func (h *Handler) Create(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": err.Error()})
		return
	}

	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "text must not be empty"})
		return
	}
	if utf8.RuneCountInString(req.Text) > 5000 {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "text exceeds 5000 characters"})
		return
	}

	if req.Voice != "" && (strings.Contains(req.Voice, "/") || strings.Contains(req.Voice, "\\")) {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "invalid voice"})
		return
	}

	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = "mp3"
	}
	if format != "mp3" {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "only mp3 format supported"})
		return
	}

	if req.Voice == "" {
		req.Voice = "zh-CN-XiaoxiaoNeural"
	}
	if req.Rate == "" {
		req.Rate = "+0%"
	}
	if req.Volume == "" {
		req.Volume = "+0%"
	}
	if req.Pitch == "" {
		req.Pitch = "+0Hz"
	}

	taskID := "tts_" + ulid.Make().String()
	t := &task.Task{
		ID:        taskID,
		Text:      req.Text,
		Voice:     req.Voice,
		Rate:      req.Rate,
		Volume:    req.Volume,
		Pitch:     req.Pitch,
		Format:    format,
		Status:    task.StatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := h.store.Save(t); err != nil {
		slog.Error("save task failed", "task_id", taskID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "store_error", "error_message": err.Error()})
		return
	}

	go h.dispatch(taskID, req, format)

	c.JSON(http.StatusOK, gin.H{
		"task_id": taskID,
		"status":  string(task.StatusProcessing),
	})
}

func (h *Handler) dispatch(taskID string, req createRequest, format string) {
	workerReq := worker.SynthesizeRequest{
		TaskID: taskID,
		Text:   req.Text,
		Voice:  req.Voice,
		Rate:   req.Rate,
		Volume: req.Volume,
		Pitch:  req.Pitch,
		Format: format,
	}

	var result *worker.SynthesizeResult
	var lastErr error

	for attempt := 0; attempt <= h.retrytimes; attempt++ {
		if attempt > 0 {
			h.store.IncrRetry(taskID)
			slog.Info("retrying worker call", "task_id", taskID, "attempt", attempt)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.workerMs)*time.Millisecond)
		result, lastErr = h.worker.Synthesize(ctx, workerReq)
		cancel()

		if lastErr == nil {
			break
		}
		slog.Warn("worker call failed", "task_id", taskID, "attempt", attempt, "error", lastErr)
	}

	if lastErr != nil {
		errorCode := "worker_failed"
		var we *worker.WorkerError
		if errors.As(lastErr, &we) {
			errorCode = we.Code
		}
		slog.Error("task failed", "task_id", taskID, "voice", req.Voice,
			"chars", utf8.RuneCountInString(req.Text), "status", "failed",
			"error_code", errorCode, "error_message", lastErr.Error())
		h.store.MarkFailed(taskID, errorCode, lastErr.Error())
		return
	}

	slog.Info("task done", "task_id", taskID, "voice", req.Voice,
		"chars", utf8.RuneCountInString(req.Text), "status", "done",
		"audio_local_path", result.AudioLocalPath, "duration_sec", result.DurationSec)
	h.store.MarkDone(taskID, result.AudioLocalPath, result.URL, result.DurationSec)
}

func (h *Handler) Result(c *gin.Context) {
	id := strings.TrimSpace(c.Query("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_code": "invalid_request", "error_message": "id is required"})
		return
	}

	t, err := h.store.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_code": "not_found", "error_message": "task not found"})
		return
	}

	resp := gin.H{
		"task_id": t.ID,
		"status":  string(t.Status),
	}
	if t.Status == task.StatusDone {
		resp["url"] = t.URL
		resp["audio_local_path"] = t.AudioLocalPath
		resp["duration_sec"] = t.DurationSec
	}
	if t.Status == task.StatusFailed {
		resp["error_code"] = t.ErrorCode
		resp["error_message"] = t.ErrorMessage
	}

	c.JSON(http.StatusOK, resp)
}
