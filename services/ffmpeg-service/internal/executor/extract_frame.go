package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
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
		args = []string{"-y", "-ss", "0", "-i", videoPath, "-vframes", "1", "-q:v", "2", outPath}
	case "tail":
		// Seek from EOF; -sseof -1 jumps to 1 second before end.
		args = []string{"-y", "-sseof", "-1", "-i", videoPath, "-vframes", "1", "-q:v", "2", outPath}
	case "time":
		timeSec, ok := getFloat64(params, "time_sec")
		if !ok {
			return "", nil, fmt.Errorf("extract-frame: time_sec required for position=time")
		}
		args = []string{"-y", "-ss", strconv.FormatFloat(timeSec, 'f', -1, 64), "-i", videoPath, "-vframes", "1", "-q:v", "2", outPath}
	default:
		return "", nil, fmt.Errorf("extract-frame: unknown position %q (want head|tail|time)", position)
	}

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
