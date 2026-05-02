package executor

import (
	"context"
	"fmt"
	"path/filepath"
)

// MergeAVExecutor merges a video track with an audio track.
//
// params:
//
//	video_path string — input video (may or may not have audio)
//	audio_path string — audio track to merge in
//	shortest   bool   — trim output to the shorter of the two inputs (default true)
type MergeAVExecutor struct{}

func (e *MergeAVExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	videoPath, ok := getString(params, "video_path")
	if !ok || videoPath == "" {
		return "", nil, fmt.Errorf("merge-av: video_path required")
	}
	audioPath, ok := getString(params, "audio_path")
	if !ok || audioPath == "" {
		return "", nil, fmt.Errorf("merge-av: audio_path required")
	}

	shortest := true
	if v, ok := getBool(params, "shortest"); ok {
		shortest = v
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+".mp4")

	args := []string{"-y", "-i", videoPath, "-i", audioPath, "-c:v", "copy", "-c:a", "aac"}
	if shortest {
		args = append(args, "-shortest")
	}
	args = append(args, outPath)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
