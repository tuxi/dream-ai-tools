package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
)

// ImagePreprocessExecutor resizes/crops/pads an image to a target resolution.
//
// params:
//
//	image_path     string — input image path
//	target_width   int    — target width in pixels (required when no filter/filter_complex)
//	target_height  int    — target height in pixels (required when no filter/filter_complex)
//	fit_mode       string — "cover" | "contain" | "fill" (default "cover")
//	output_format  string — output image format (default "jpg")
//	output_path    string — override auto-generated output path
//	filter         string — raw -vf filter (overrides fit_mode; requires output_path or format)
//	filter_complex string — raw -filter_complex (overrides filter and fit_mode)
//	jpeg_quality   int    — -q:v value for JPEG output (default 2)
type ImagePreprocessExecutor struct{}

func (e *ImagePreprocessExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	imagePath, ok := getString(params, "image_path")
	if !ok || imagePath == "" {
		return "", nil, fmt.Errorf("image-preprocess: image_path required")
	}

	format := "jpg"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}
	if format == "jpeg" {
		format = "jpg"
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)
	if p, ok := getString(params, "output_path"); ok && p != "" {
		outPath = p
	}

	jpegQuality := 2
	if q, ok := getInt(params, "jpeg_quality"); ok && q >= 2 && q <= 31 {
		jpegQuality = q
	}

	// Build extra quality args for JPEG.
	qualityArgs := []string{"-frames:v", "1"}
	if format == "jpg" {
		qualityArgs = append(qualityArgs, "-q:v", strconv.Itoa(jpegQuality))
	}

	// Raw filter_complex path.
	if fc, ok := getString(params, "filter_complex"); ok && fc != "" {
		args := append([]string{"-y", "-i", imagePath, "-filter_complex", fc}, qualityArgs...)
		args = append(args, outPath)
		if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
			return "", nil, err
		}
		return outPath, nil, nil
	}

	// Raw -vf filter path.
	if vf, ok := getString(params, "filter"); ok && vf != "" {
		args := append([]string{"-y", "-i", imagePath, "-vf", vf}, qualityArgs...)
		args = append(args, outPath)
		if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
			return "", nil, err
		}
		return outPath, nil, nil
	}

	// Mode-based path.
	w, okW := getInt(params, "target_width")
	h, okH := getInt(params, "target_height")
	if !okW || !okH || w <= 0 || h <= 0 {
		return "", nil, fmt.Errorf("image-preprocess: target_width and target_height required")
	}

	fitMode := "cover"
	if m, ok := getString(params, "fit_mode"); ok && m != "" {
		fitMode = m
	}

	var vf string
	switch fitMode {
	case "cover":
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", w, h, w, h)
	case "contain":
		vf = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", w, h, w, h)
	case "fill":
		vf = fmt.Sprintf("scale=%d:%d", w, h)
	default:
		return "", nil, fmt.Errorf("image-preprocess: unknown fit_mode %q (want cover|contain|fill)", fitMode)
	}

	args := append([]string{"-y", "-i", imagePath, "-vf", vf}, qualityArgs...)
	args = append(args, outPath)
	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
