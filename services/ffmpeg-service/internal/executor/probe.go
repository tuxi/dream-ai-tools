package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// ProbeInfo holds the result of a ffprobe call.
type ProbeInfo struct {
	DurationSec float64       `json:"duration_sec"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	SizeBytes   int64         `json:"size_bytes"`
	Streams     []StreamInfo  `json:"streams"`
}

// StreamInfo describes a single audio or video stream.
type StreamInfo struct {
	CodecType  string  `json:"codec_type"`
	CodecName  string  `json:"codec_name"`
	FPS        float64 `json:"fps,omitempty"`
	SampleRate int     `json:"sample_rate,omitempty"`
}

// Probe runs ffprobe synchronously and returns media metadata.
func Probe(ctx context.Context, ffprobePath, path string) (*ProbeInfo, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var raw struct {
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
		} `json:"format"`
		Streams []struct {
			CodecType         string `json:"codec_type"`
			CodecName         string `json:"codec_name"`
			Width             int    `json:"width"`
			Height            int    `json:"height"`
			RFrameRate        string `json:"r_frame_rate"`
			SampleRate        string `json:"sample_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}

	info := &ProbeInfo{}

	if raw.Format.Duration != "" {
		info.DurationSec, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	}
	if raw.Format.Size != "" {
		info.SizeBytes, _ = strconv.ParseInt(raw.Format.Size, 10, 64)
	}

	for _, s := range raw.Streams {
		si := StreamInfo{
			CodecType: s.CodecType,
			CodecName: s.CodecName,
		}
		if s.CodecType == "video" {
			if info.Width == 0 {
				info.Width = s.Width
				info.Height = s.Height
			}
			si.FPS = parseFrameRate(s.RFrameRate)
		}
		if s.CodecType == "audio" && s.SampleRate != "" {
			si.SampleRate, _ = strconv.Atoi(s.SampleRate)
		}
		info.Streams = append(info.Streams, si)
	}

	return info, nil
}

// parseFrameRate parses "30000/1001" or "25/1" style frame rate strings.
func parseFrameRate(s string) float64 {
	if s == "" {
		return 0
	}
	var num, den float64
	if _, err := fmt.Sscanf(s, "%f/%f", &num, &den); err == nil && den != 0 {
		return num / den
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
