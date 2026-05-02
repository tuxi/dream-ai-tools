package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
)

// FramesToVideoExecutor assembles an image frame sequence into a video.
//
// params:
//
//	frame_dir     string  — directory containing frames
//	frame_pattern string  — printf pattern, e.g. "frame_%04d.jpg"
//	fps           int     — frames per second (default 25)
//	width         int     — output width (0 = keep source)
//	height        int     — output height (0 = keep source)
//	output_format string  — output container (default "mp4")
type FramesToVideoExecutor struct{}

func (e *FramesToVideoExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	frameDir, ok := getString(params, "frame_dir")
	if !ok || frameDir == "" {
		return "", nil, fmt.Errorf("frames-to-video: frame_dir required")
	}
	pattern, ok := getString(params, "frame_pattern")
	if !ok || pattern == "" {
		return "", nil, fmt.Errorf("frames-to-video: frame_pattern required")
	}

	fps := 25
	if v, ok := getInt(params, "fps"); ok && v > 0 {
		fps = v
	}

	format := "mp4"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}

	inputPattern := filepath.Join(frameDir, pattern)
	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)

	args := []string{"-y", "-framerate", strconv.Itoa(fps), "-i", inputPattern}

	width, hasW := getInt(params, "width")
	height, hasH := getInt(params, "height")
	if hasW && hasH && width > 0 && height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", width, height))
	}

	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", outPath)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
