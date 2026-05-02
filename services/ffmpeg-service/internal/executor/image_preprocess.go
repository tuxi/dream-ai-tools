package executor

import (
	"context"
	"fmt"
	"path/filepath"
)

// ImagePreprocessExecutor resizes/crops/pads an image to a target resolution.
//
// params:
//
//	image_path     string — input image path
//	target_width   int    — target width in pixels
//	target_height  int    — target height in pixels
//	fit_mode       string — "cover" | "contain" | "fill" (default "cover")
//	output_format  string — output image format (default "jpg")
type ImagePreprocessExecutor struct{}

func (e *ImagePreprocessExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	imagePath, ok := getString(params, "image_path")
	if !ok || imagePath == "" {
		return "", nil, fmt.Errorf("image-preprocess: image_path required")
	}

	w, okW := getInt(params, "target_width")
	h, okH := getInt(params, "target_height")
	if !okW || !okH || w <= 0 || h <= 0 {
		return "", nil, fmt.Errorf("image-preprocess: target_width and target_height required")
	}

	fitMode := "cover"
	if m, ok := getString(params, "fit_mode"); ok && m != "" {
		fitMode = m
	}

	format := "jpg"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)

	var vf string
	switch fitMode {
	case "cover":
		// Scale up to fill, then crop to exact dimensions.
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", w, h, w, h)
	case "contain":
		// Scale down to fit within, then pad remaining area.
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", w, h, w, h)
	case "fill":
		// Stretch to exact dimensions (may distort).
		vf = fmt.Sprintf("scale=%d:%d", w, h)
	default:
		return "", nil, fmt.Errorf("image-preprocess: unknown fit_mode %q (want cover|contain|fill)", fitMode)
	}

	if err := runFFmpeg(ctx, cfg.FFmpegPath,
		"-y", "-i", imagePath, "-vf", vf, "-frames:v", "1", outPath,
	); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
