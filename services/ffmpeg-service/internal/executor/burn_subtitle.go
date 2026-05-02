package executor

import (
	"context"
	"fmt"
	"path/filepath"
)

// BurnSubtitleExecutor burns an ASS/SRT subtitle file into a video.
//
// params:
//
//	video_path     string — input video path
//	subtitle_path  string — subtitle file path (.ass or .srt)
//	style_override string — optional ASS force_style override string
type BurnSubtitleExecutor struct{}

func (e *BurnSubtitleExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	videoPath, ok := getString(params, "video_path")
	if !ok || videoPath == "" {
		return "", nil, fmt.Errorf("burn-subtitle: video_path required")
	}
	subtitlePath, ok := getString(params, "subtitle_path")
	if !ok || subtitlePath == "" {
		return "", nil, fmt.Errorf("burn-subtitle: subtitle_path required")
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+".mp4")

	// Escape colon in path for the filter string (Windows paths or unusual chars).
	escapedSub := escapeFilterPath(subtitlePath)
	vf := "ass=" + escapedSub

	if style, ok := getString(params, "style_override"); ok && style != "" {
		vf = fmt.Sprintf("ass=%s:force_style='%s'", escapedSub, style)
	}

	if err := runFFmpeg(ctx, cfg.FFmpegPath,
		"-y", "-i", videoPath,
		"-vf", vf,
		"-c:a", "copy",
		outPath,
	); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}

// escapeFilterPath escapes a file path for use inside an ffmpeg filtergraph.
func escapeFilterPath(p string) string {
	// Colons and backslashes are special characters in ffmpeg filter strings.
	out := ""
	for _, c := range p {
		switch c {
		case '\\':
			out += "\\\\"
		case ':':
			out += "\\:"
		default:
			out += string(c)
		}
	}
	return out
}
