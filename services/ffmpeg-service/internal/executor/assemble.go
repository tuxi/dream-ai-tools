package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// AssembleExecutor handles audio-visual assembly: single-video+audio, concat+audio,
// and pre-built filter_complex (xfade) paths.
//
// params:
//
//	video_paths       []string — direct -i video inputs (used for xfade / single-video paths)
//	concat_videos     []string — video list for concat demuxer (input 0 = concat; audio_path = input 1)
//	audio_path        string   — optional extra audio input (appended after video inputs)
//	filter_complex    string   — pre-built filter_complex
//	map_video_spec    string   — -map target for video output (default "0:v:0")
//	map_audio_spec    string   — -map target for audio output; empty = -an
//	output_path       string   — required output file path
//	video_codec       string   — default "libx264"
//	video_preset      string   — default "fast"
//	video_crf         int      — default 23
//	audio_codec       string   — default "aac"
//	audio_bitrate     string   — default "128k"
//	audio_samplerate  int      — default 44100
//	movflags_faststart bool    — add -movflags +faststart
type AssembleExecutor struct{}

func (e *AssembleExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	outPath, ok := getString(params, "output_path")
	if !ok || outPath == "" {
		outPath = filepath.Join(cfg.WorkDir, jobID+".mp4")
	}

	// ── Build input args ──────────────────────────────────────────────────

	args := []string{"-y"}

	concatVideos, hasConcatVideos := getStringSlice(params, "concat_videos")
	videoPaths, hasVideoPaths := getStringSlice(params, "video_paths")
	audioPath, _ := getString(params, "audio_path")

	var listFile string

	if hasConcatVideos && len(concatVideos) > 0 {
		// Concat-demuxer mode: write a temporary list file, use -f concat as input 0.
		var err error
		listFile, err = writeConcatList(concatVideos, cfg.WorkDir, jobID)
		if err != nil {
			return "", nil, fmt.Errorf("assemble: write concat list: %w", err)
		}
		defer os.Remove(listFile)

		args = append(args, "-f", "concat", "-safe", "0", "-i", listFile)
	} else if hasVideoPaths && len(videoPaths) > 0 {
		for _, p := range videoPaths {
			args = append(args, "-i", p)
		}
	}

	if audioPath != "" {
		args = append(args, "-i", audioPath)
	}

	// ── filter_complex ────────────────────────────────────────────────────

	fc, hasFC := getString(params, "filter_complex")
	if hasFC && fc != "" {
		args = append(args, "-filter_complex", fc)
	}

	// ── map specs ────────────────────────────────────────────────────────

	mapVideo, _ := getString(params, "map_video_spec")
	if mapVideo == "" {
		mapVideo = "0:v:0"
	}
	args = append(args, "-map", mapVideo)

	mapAudio, hasMapAudio := getString(params, "map_audio_spec")
	if hasMapAudio && mapAudio != "" {
		args = append(args, "-map", mapAudio)
	} else {
		args = append(args, "-an")
	}

	// ── encoding ─────────────────────────────────────────────────────────

	videoCodec := "libx264"
	if c, ok := getString(params, "video_codec"); ok && c != "" {
		videoCodec = c
	}
	videoPreset := "fast"
	if p, ok := getString(params, "video_preset"); ok && p != "" {
		videoPreset = p
	}
	videoCRF := 23
	if c, ok := getInt(params, "video_crf"); ok && c > 0 {
		videoCRF = c
	}

	args = append(args,
		"-c:v", videoCodec,
		"-preset", videoPreset,
		"-crf", strconv.Itoa(videoCRF),
		"-pix_fmt", "yuv420p",
	)

	if hasMapAudio && mapAudio != "" {
		audioCodec := "aac"
		if c, ok := getString(params, "audio_codec"); ok && c != "" {
			audioCodec = c
		}
		audioBitrate := "128k"
		if b, ok := getString(params, "audio_bitrate"); ok && b != "" {
			audioBitrate = b
		}
		audioSampleRate := 44100
		if r, ok := getInt(params, "audio_samplerate"); ok && r > 0 {
			audioSampleRate = r
		}
		args = append(args,
			"-c:a", audioCodec,
			"-b:a", audioBitrate,
			"-ar", strconv.Itoa(audioSampleRate),
		)
	}

	if v, ok := getBool(params, "movflags_faststart"); ok && v {
		args = append(args, "-movflags", "+faststart")
	}

	args = append(args, outPath)

	if err := runFFmpeg(ctx, cfg.FFmpegPath, args...); err != nil {
		return "", nil, err
	}
	return outPath, nil, nil
}
