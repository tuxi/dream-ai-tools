package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// PostprocessExecutor resizes, watermarks, and/or re-encodes a video.
//
// params:
//
//	video_path            string         — input video path (required)
//	output_path           string         — override auto-generated output path
//	target_width          int            — resize target width  (0 = no resize)
//	target_height         int            — resize target height (0 = no resize)
//	keep_audio            bool           — preserve audio track (default true)
//	has_audio             bool           — source has an audio track (default true)
//	watermark_text        string         — text overlay, bottom-right (drawtext)
//	watermark_image_path  string         — image overlay, bottom-right (overlay)
//	drawtext_params       map[string]any — raw drawtext key=value pairs (overrides watermark_text)
type PostprocessExecutor struct{}

func (e *PostprocessExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	videoPath, ok := getString(params, "video_path")
	if !ok || videoPath == "" {
		return "", nil, fmt.Errorf("postprocess: video_path required")
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+".mp4")
	if p, ok := getString(params, "output_path"); ok && p != "" {
		outPath = p
	}

	targetW, _ := getInt(params, "target_width")
	targetH, _ := getInt(params, "target_height")
	needResize := targetW > 0 && targetH > 0

	keepAudio := true
	if v, ok := getBool(params, "keep_audio"); ok {
		keepAudio = v
	}
	hasAudio := true
	if v, ok := getBool(params, "has_audio"); ok {
		hasAudio = v
	}

	drawtextParams, hasRaw := getMap(params, "drawtext_params")
	watermarkText, _ := getString(params, "watermark_text")
	watermarkImage, _ := getString(params, "watermark_image_path")
	needWatermark := (hasRaw && len(drawtextParams) > 0) || watermarkText != "" || watermarkImage != ""

	// audioArgs is appended to every re-encode path.
	audioArgs := []string{"-c:a", "aac", "-b:a", "128k"}
	if !keepAudio || !hasAudio {
		audioArgs = []string{"-an"}
	}

	// ── Stream-copy path (no filter needed) ───────────────────────────────
	if !needResize && !needWatermark {
		args := []string{"-y", "-i", videoPath}
		if keepAudio {
			args = append(args, "-c", "copy")
		} else {
			args = append(args, "-map", "0:v:0", "-c:v", "copy", "-an")
		}
		args = append(args, "-movflags", "+faststart", outPath)
		if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
			return "", nil, err
		}
		return outPath, nil, nil
	}

	// ── Build the video filter chain ──────────────────────────────────────
	//
	// Order matches the original tool:
	//   1. scale+pad (contain-fit, black bars)  — only when resizing
	//   2. setsar=1  (SAR correction)            — always
	//   3. drawtext  (text watermark)            — when requested
	//
	// watermark_image uses filter_complex instead (see below).

	var vfParts []string

	if needResize {
		vfParts = append(vfParts, fmt.Sprintf(
			"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1",
			targetW, targetH, targetW, targetH,
		))
	} else {
		vfParts = append(vfParts, "setsar=1")
	}

	switch {
	case hasRaw && len(drawtextParams) > 0:
		vfParts = append(vfParts, "drawtext="+serializeDrawtext(drawtextParams))
	case watermarkText != "":
		// Style matches ai-engine/workflows/videos/video_postprocess.go.
		vfParts = append(vfParts, fmt.Sprintf(
			"drawtext=text='%s':x=w-tw-24:y=h-th-24:fontsize=24:fontcolor=white:box=1:boxcolor=black@0.35:boxborderw=8",
			escapeDrawtext(watermarkText),
		))
	}

	encodeArgs := []string{
		"-c:v", "libx264",
		"-preset", "slow",
		"-crf", "18",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
	}

	// ── watermark_image: use filter_complex to combine resize + overlay ───
	if watermarkImage != "" {
		var complexFilter string
		if needResize {
			complexFilter = fmt.Sprintf(
				"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1[v];[v][1:v]overlay=W-w-10:H-h-10[out]",
				targetW, targetH, targetW, targetH,
			)
		} else {
			complexFilter = "[0:v]setsar=1[v];[v][1:v]overlay=W-w-10:H-h-10[out]"
		}
		args := append([]string{"-y", "-i", videoPath, "-i", watermarkImage,
			"-filter_complex", complexFilter, "-map", "[out]"},
			encodeArgs...,
		)
		args = append(args, audioArgs...)
		args = append(args, outPath)
		if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
			return "", nil, err
		}
		return outPath, nil, nil
	}

	// ── Standard -vf path (resize and/or drawtext) ────────────────────────
	vf := strings.Join(vfParts, ",")
	args := append([]string{"-y", "-i", videoPath, "-vf", vf}, encodeArgs...)
	args = append(args, audioArgs...)
	args = append(args, outPath)
	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}

// serializeDrawtext converts a map to "key=value:key=value" for the drawtext filter.
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

// escapeDrawtext escapes special characters in drawtext text values.
// Matches the escaping in ai-engine/workflows/videos/video_postprocess.go.
func escapeDrawtext(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
		`'`, `\'`,
		`[`, `\[`,
		`]`, `\]`,
		`%`, `\%`,
	)
	return replacer.Replace(s)
}
