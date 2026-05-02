package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ImageToFramesExecutor converts a still image to a frame sequence.
//
// params:
//
//	image_path   string  — source image path
//	fps          int     — frames per second (default 25)
//	duration_sec float64 — total duration in seconds (default 3.0)
//	output_dir   string  — destination directory for frames
type ImageToFramesExecutor struct{}

func (e *ImageToFramesExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	imagePath, ok := getString(params, "image_path")
	if !ok || imagePath == "" {
		return "", nil, fmt.Errorf("image-to-frames: image_path required")
	}

	outputDir, ok := getString(params, "output_dir")
	if !ok || outputDir == "" {
		outputDir = filepath.Join(cfg.WorkDir, jobID+"_frames")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("image-to-frames: mkdir output_dir: %w", err)
	}

	fps := 25
	if v, ok := getInt(params, "fps"); ok && v > 0 {
		fps = v
	}

	durSec := 3.0
	if v, ok := getFloat64(params, "duration_sec"); ok && v > 0 {
		durSec = v
	}

	outPattern := filepath.Join(outputDir, "frame_%04d.jpg")
	if err := runFFmpeg(ctx, cfg.FFmpegPath,
		"-y",
		"-loop", "1",
		"-i", imagePath,
		"-t", strconv.FormatFloat(durSec, 'f', -1, 64),
		"-r", strconv.Itoa(fps),
		"-q:v", "2",
		outPattern,
	); err != nil {
		return "", nil, err
	}

	return outputDir, nil, nil
}
