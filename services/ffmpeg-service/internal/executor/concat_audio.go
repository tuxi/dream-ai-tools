package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ConcatAudioExecutor concatenates multiple audio segments in order.
//
// Simple path (no filter_complex):
//
//	audio_paths   []string — ordered list of audio file paths
//	gap_sec       float64  — silence gap between segments (ignored; reserved for future use)
//	output_format string   — output container (default "mp3")
//
// filter_complex path (pre-built by caller):
//
//	filter_complex  string   — full filter_complex string; triggers this path
//	inputs          []object — ordered inputs: {"type":"file","path":"..."} or
//	                           {"type":"lavfi","duration":1.5,"source":"anullsrc=r=44100:cl=mono"}
//	map_spec        string   — output label to -map (default "[aout]")
//	audio_codec     string   — default "libmp3lame"
//	audio_bitrate   string   — default "128k"
//	audio_samplerate int     — default 44100
//	output_path     string   — optional; overrides workDir/jobID.format
//	output_format   string   — output container when output_path is empty (default "mp3")
type ConcatAudioExecutor struct{}

func (e *ConcatAudioExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	// filter_complex path: caller supplies pre-built filter + mixed inputs (file + lavfi).
	if fc, ok := getString(params, "filter_complex"); ok && fc != "" {
		return e.runWithFilterComplex(ctx, params, jobID, cfg, fc)
	}

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

// runWithFilterComplex handles the pre-built filter_complex path for concat-audio.
func (e *ConcatAudioExecutor) runWithFilterComplex(ctx context.Context, params map[string]any, jobID string, cfg Config, fc string) (string, []string, error) {
	format := "mp3"
	if f, ok := getString(params, "output_format"); ok && f != "" {
		format = f
	}
	outPath := filepath.Join(cfg.WorkDir, jobID+"."+format)
	if op, ok := getString(params, "output_path"); ok && op != "" {
		outPath = op
	}

	args := []string{"-y"}

	rawInputs, _ := params["inputs"].([]interface{})
	for _, inp := range rawInputs {
		m, ok := inp.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "lavfi" {
			dur, _ := m["duration"].(float64)
			src, _ := m["source"].(string)
			if src == "" {
				src = "anullsrc=r=44100:cl=mono"
			}
			args = append(args, "-f", "lavfi", "-t", fmt.Sprintf("%.3f", dur), "-i", src)
		} else {
			path, _ := m["path"].(string)
			args = append(args, "-i", path)
		}
	}

	mapSpec := "[aout]"
	if ms, ok := getString(params, "map_spec"); ok && ms != "" {
		mapSpec = ms
	}
	args = append(args, "-filter_complex", fc, "-map", mapSpec)

	audioCodec := "libmp3lame"
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
	args = append(args, "-c:a", audioCodec, "-ar", strconv.Itoa(audioSampleRate), "-b:a", audioBitrate, outPath)

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
