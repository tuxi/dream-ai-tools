package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConcatAudioExecutor concatenates multiple audio segments in order.
//
// params:
//
//	audio_paths   []string — ordered list of audio file paths
//	gap_sec       float64  — silence gap between segments (ignored; reserved for future use)
//	output_format string   — output container (default "mp3")
type ConcatAudioExecutor struct{}

func (e *ConcatAudioExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	paths, ok := getStringSlice(params, "audio_paths")
	if !ok || len(paths) == 0 {
		return "", nil, fmt.Errorf("concat-audio: audio_paths required")
	}

	format := "mp3"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)

	gapSec, hasGap := getFloat64(params, "gap_sec")
	if !hasGap || gapSec <= 0 {
		// Lossless concat via demuxer — fastest path.
		listFile, err := writeConcatList(paths, cfg.WorkDir, jobID)
		if err != nil {
			return "", nil, fmt.Errorf("concat-audio: write list: %w", err)
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

	// Re-encode path with filter_complex concat (no silence insertion for now).
	args := []string{"-y"}
	for _, p := range paths {
		args = append(args, "-i", p)
	}

	n := len(paths)
	inputs := make([]string, n)
	for i := range paths {
		inputs[i] = fmt.Sprintf("[%d:a]", i)
	}
	filter := strings.Join(inputs, "") + fmt.Sprintf("concat=n=%d:v=0:a=1[out]", n)
	args = append(args, "-filter_complex", filter, "-map", "[out]", outPath)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}

// writeConcatList writes an ffmpeg concat demuxer list file and returns its path.
func writeConcatList(paths []string, workDir, jobID string) (string, error) {
	f, err := os.CreateTemp(workDir, jobID+"_concat_*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()

	for _, p := range paths {
		fmt.Fprintf(f, "file '%s'\n", p)
	}
	return f.Name(), nil
}
