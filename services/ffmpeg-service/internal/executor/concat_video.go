package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConcatVideoExecutor concatenates multiple video segments.
//
// params:
//
//	video_paths    []string — ordered list of video file paths
//	reencode       bool     — true: re-encode via filter_complex; false: concat demuxer (lossless)
//	filter_complex string   — pre-built filter_complex string (overrides reencode logic)
//	map_spec       string   — output map label used with filter_complex (e.g. "[vout]")
type ConcatVideoExecutor struct{}

func (e *ConcatVideoExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	paths, ok := getStringSlice(params, "video_paths")
	if !ok || len(paths) == 0 {
		return "", nil, fmt.Errorf("concat-video: video_paths required")
	}

	reencode, _ := getBool(params, "reencode")
	outPath := filepath.Join(cfg.WorkDir, jobID+".mp4")

	// Pre-built filter_complex path (used by merge_video with xfade transitions).
	if fc, ok := getString(params, "filter_complex"); ok && fc != "" {
		mapSpec, _ := getString(params, "map_spec")
		if mapSpec == "" {
			mapSpec = "[vout]"
		}
		args := []string{"-y"}
		for _, p := range paths {
			args = append(args, "-i", p)
		}
		args = append(args,
			"-filter_complex", fc,
			"-map", mapSpec,
			"-c:v", "libx264",
			"-preset", "medium",
			"-pix_fmt", "yuv420p",
			"-movflags", "+faststart",
			outPath,
		)
		if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
			return "", nil, err
		}
		return outPath, nil, nil
	}

	if !reencode {
		listFile, err := writeConcatList(paths, cfg.WorkDir, jobID)
		if err != nil {
			return "", nil, fmt.Errorf("concat-video: write list: %w", err)
		}
		defer os.Remove(listFile)

		if err := runFFmpeg(ctx, cfg.FFmpegPath,
			"-y", "-f", "concat", "-safe", "0", "-i", listFile,
			"-c", "copy", outPath,
		); err != nil {
			return "", nil, err
		}
		return outPath, nil, nil
	}

	// Re-encode: build filter_complex [0:v][0:a][1:v][1:a]...concat=n=N:v=1:a=1[outv][outa]
	args := []string{"-y"}
	for _, p := range paths {
		args = append(args, "-i", p)
	}

	n := len(paths)
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("[%d:v][%d:a]", i, i))
	}
	filter := strings.Join(parts, "") + fmt.Sprintf("concat=n=%d:v=1:a=1[outv][outa]", n)
	args = append(args,
		"-filter_complex", filter,
		"-map", "[outv]", "-map", "[outa]",
		outPath,
	)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
