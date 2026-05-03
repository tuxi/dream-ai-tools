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
//	image_path    string  — source image path
//	fps           int     — frames per second (default 25)
//	duration_sec  float64 — total duration in seconds (default 3.0); ignored when frame_count > 0
//	output_dir    string  — destination directory for frames
//	filter        string  — raw -vf filter (overrides simple loop)
//	output_format string  — frame file extension: "jpg" or "png" (default "jpg")
//	frame_count   int     — if > 0, use -frames:v N instead of -t duration
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

	format := "jpg"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}

	outPattern := filepath.Join(outputDir, "frame_%04d."+format)

	args := []string{"-y", "-loop", "1", "-i", imagePath}

	if vf, ok := getString(params, "filter"); ok && vf != "" {
		args = append(args, "-vf", vf)
	}

	args = append(args, "-pix_fmt", "yuv420p")

	if frameCount, ok := getInt(params, "frame_count"); ok && frameCount > 0 {
		args = append(args, "-frames:v", strconv.Itoa(frameCount))
	} else {
		durSec := 3.0
		if v, ok := getFloat64(params, "duration_sec"); ok && v > 0 {
			durSec = v
		}
		args = append(args, "-t", strconv.FormatFloat(durSec, 'f', -1, 64),
			"-r", strconv.Itoa(fps))
	}

	args = append(args, outPattern)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}

	// Collect frame paths for the caller.
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return outputDir, nil, nil
	}
	var framePaths []string
	for _, e := range entries {
		if !e.IsDir() {
			framePaths = append(framePaths, filepath.Join(outputDir, e.Name()))
		}
	}

	return outputDir, framePaths, nil
}
