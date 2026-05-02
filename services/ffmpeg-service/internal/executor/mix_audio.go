package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
)

// MixAudioExecutor mixes a TTS track with a BGM track.
//
// params:
//
//	tts_path      string  — foreground audio path
//	bgm_path      string  — background music path
//	tts_volume    float64 — foreground volume multiplier (default 1.0)
//	bgm_volume    float64 — background volume multiplier (default 0.3)
//	duration_sec  float64 — trim total output to this duration
//	output_format string  — output container (default "mp3")
type MixAudioExecutor struct{}

func (e *MixAudioExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	ttsPath, ok := getString(params, "tts_path")
	if !ok || ttsPath == "" {
		return "", nil, fmt.Errorf("mix-audio: tts_path required")
	}
	bgmPath, ok := getString(params, "bgm_path")
	if !ok || bgmPath == "" {
		return "", nil, fmt.Errorf("mix-audio: bgm_path required")
	}

	ttsVol := 1.0
	if v, ok := getFloat64(params, "tts_volume"); ok {
		ttsVol = v
	}
	bgmVol := 0.3
	if v, ok := getFloat64(params, "bgm_volume"); ok {
		bgmVol = v
	}

	durSec, hasDur := getFloat64(params, "duration_sec")

	format := "mp3"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}

	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)

	filter := fmt.Sprintf(
		"[0]volume=%s[a];[1]volume=%s[b];[a][b]amix=inputs=2:duration=first",
		strconv.FormatFloat(ttsVol, 'f', -1, 64),
		strconv.FormatFloat(bgmVol, 'f', -1, 64),
	)
	if hasDur {
		filter = fmt.Sprintf(
			"[0]volume=%s[a];[1]volume=%s,atrim=0:%s[b];[a][b]amix=inputs=2:duration=first",
			strconv.FormatFloat(ttsVol, 'f', -1, 64),
			strconv.FormatFloat(bgmVol, 'f', -1, 64),
			strconv.FormatFloat(durSec, 'f', -1, 64),
		)
	}

	args := []string{"-y", "-i", ttsPath, "-i", bgmPath, "-filter_complex", filter}
	if hasDur {
		args = append(args, "-t", strconv.FormatFloat(durSec, 'f', -1, 64))
	}
	args = append(args, outPath)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
