package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExtractFrameExecutor extracts a single frame from a video.
//
// params:
//
//	video_path    string  — input video path
//	position      string  — "head" | "tail" | "time"
//	time_sec      float64 — required when position="time"
//	output_format string  — output image format (default "jpg")
type ExtractFrameExecutor struct{}

func (e *ExtractFrameExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	videoPath, ok := getString(params, "video_path")
	if !ok || videoPath == "" {
		return "", nil, fmt.Errorf("extract-frame: video_path required")
	}

	position, _ := getString(params, "position")
	if position == "" {
		position = "head"
	}

	format := "jpg"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)

	var args []string
	switch position {
	case "head":
		args = []string{"-y", "-ss", "0", "-i", videoPath, "-vframes", "1", "-q:v", "2"}
	case "tail":
		// Seek from EOF; -sseof -1 jumps to 1 second before end.
		args = []string{"-y", "-sseof", "-1", "-i", videoPath, "-vframes", "1", "-q:v", "2"}
	case "time":
		timeSec, ok := getFloat64(params, "time_sec")
		if !ok {
			return "", nil, fmt.Errorf("extract-frame: time_sec required for position=time")
		}
		args = []string{"-y", "-ss", strconv.FormatFloat(timeSec, 'f', -1, 64), "-i", videoPath, "-vframes", "1", "-q:v", "2"}
	default:
		return "", nil, fmt.Errorf("extract-frame: unknown position %q (want head|tail|time)", position)
	}
	args = append(args, extractFrameOutputArgs(format)...)
	args = append(args, outPath)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	if err := validateFrameOutput(outPath); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}

func extractFrameOutputArgs(format string) []string {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), ".")) {
	case "jpg", "jpeg":
		// FFmpeg 8.x can fail to initialize the mjpeg encoder for tv-range
		// yuv420p frames unless the JPEG pixel format is explicit.
		return []string{"-pix_fmt", "yuvj420p", "-threads", "1"}
	default:
		return nil
	}
}

func validateFrameOutput(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("extract-frame: output missing path=%s: %w", path, err)
	}
	if info.Size() <= 0 {
		return fmt.Errorf("extract-frame: empty output path=%s", path)
	}
	return nil
}
