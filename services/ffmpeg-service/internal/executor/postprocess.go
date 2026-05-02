package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// PostprocessExecutor applies watermark/drawtext overlays to a video.
//
// params:
//
//	video_path            string         — input video path
//	watermark_text        string         — text overlay (uses drawtext filter)
//	watermark_image_path  string         — image overlay (uses overlay filter)
//	drawtext_params       map[string]any — raw drawtext key=value pairs (override watermark_text)
type PostprocessExecutor struct{}

func (e *PostprocessExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	videoPath, ok := getString(params, "video_path")
	if !ok || videoPath == "" {
		return "", nil, fmt.Errorf("postprocess: video_path required")
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+".mp4")

	drawtextParams, hasRaw := getMap(params, "drawtext_params")
	watermarkText, _ := getString(params, "watermark_text")
	watermarkImage, _ := getString(params, "watermark_image_path")

	switch {
	case hasRaw && len(drawtextParams) > 0:
		vf := "drawtext=" + serializeDrawtext(drawtextParams)
		if err := runFFmpeg(ctx, cfg.FFmpegPath,
			"-y", "-i", videoPath, "-vf", vf, "-c:a", "copy", outPath,
		); err != nil {
			return "", nil, err
		}

	case watermarkText != "":
		vf := fmt.Sprintf(
			"drawtext=text='%s':fontcolor=white:fontsize=36:x=10:y=10:box=1:boxcolor=black@0.5:boxborderw=5",
			strings.ReplaceAll(watermarkText, "'", "\\'"),
		)
		if err := runFFmpeg(ctx, cfg.FFmpegPath,
			"-y", "-i", videoPath, "-vf", vf, "-c:a", "copy", outPath,
		); err != nil {
			return "", nil, err
		}

	case watermarkImage != "":
		// Overlay image in bottom-right with 10px margin.
		filter := "[0:v][1:v]overlay=W-w-10:H-h-10"
		if err := runFFmpeg(ctx, cfg.FFmpegPath,
			"-y", "-i", videoPath, "-i", watermarkImage,
			"-filter_complex", filter,
			"-c:a", "copy", outPath,
		); err != nil {
			return "", nil, err
		}

	default:
		// No-op: stream-copy passthrough.
		if err := runFFmpeg(ctx, cfg.FFmpegPath,
			"-y", "-i", videoPath, "-c", "copy", outPath,
		); err != nil {
			return "", nil, err
		}
	}

	return outPath, nil, nil
}

// serializeDrawtext converts a map to "key=value:key=value" drawtext parameter string.
func serializeDrawtext(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(m))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ":")
}
