package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/tuxi/dream-ai-tools/ffmpeg-service/internal/job"
)

// Config holds paths and working directory for all executors.
type Config struct {
	WorkDir     string
	FFmpegPath  string
	FFprobePath string
}

// Executor runs a single ffmpeg operation.
type Executor interface {
	Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (outputPath string, outputPaths []string, err error)
}

var registry = map[string]Executor{
	"mix-audio":        &MixAudioExecutor{},
	"concat-audio":     &ConcatAudioExecutor{},
	"concat-video":     &ConcatVideoExecutor{},
	"frames-to-video":  &FramesToVideoExecutor{},
	"image-to-frames":  &ImageToFramesExecutor{},
	"merge-av":         &MergeAVExecutor{},
	"burn-subtitle":    &BurnSubtitleExecutor{},
	"extract-frame":    &ExtractFrameExecutor{},
	"postprocess":      &PostprocessExecutor{},
	"image-preprocess": &ImagePreprocessExecutor{},
	"assemble":         &AssembleExecutor{},
}

// Dispatch routes the job to the appropriate executor.
func Dispatch(ctx context.Context, j *job.Job, cfg Config) (string, []string, error) {
	exec, ok := registry[j.Operation]
	if !ok {
		return "", nil, fmt.Errorf("unknown operation: %s", j.Operation)
	}
	return exec.Run(ctx, j.Params, j.ID, cfg)
}

// KnownOperations returns all registered operation names.
func KnownOperations() []string {
	ops := make([]string, 0, len(registry))
	for k := range registry {
		ops = append(ops, k)
	}
	return ops
}

// runFFmpeg executes an ffmpeg command and returns combined output on failure.
func runFFmpeg(ctx context.Context, ffmpegPath string, args ...string) error {
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w; output: %s", err, string(out))
	}
	if len(out) > 0 {
		slog.Info("ffmpeg completed", "output", string(out))
	}
	return nil
}

// param helpers — JSON numbers unmarshal as float64 in map[string]any.

func getString(params map[string]any, key string) (string, bool) {
	v, ok := params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func getFloat64(params map[string]any, key string) (float64, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch f := v.(type) {
	case float64:
		return f, true
	case int:
		return float64(f), true
	case int64:
		return float64(f), true
	}
	return 0, false
}

func getInt(params map[string]any, key string) (int, bool) {
	f, ok := getFloat64(params, key)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func getBool(params map[string]any, key string) (bool, bool) {
	v, ok := params[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func getStringSlice(params map[string]any, key string) ([]string, bool) {
	v, ok := params[key]
	if !ok {
		return nil, false
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func getMap(params map[string]any, key string) (map[string]any, bool) {
	v, ok := params[key]
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}
