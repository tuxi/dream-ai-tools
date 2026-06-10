package executor

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
)

// DetectScenesExecutor detects scene boundaries with ffmpeg's scene filter.
//
// params:
//
//	video_path          string  — input video path
//	threshold           float64 — scene threshold, default 0.3
//	min_scene_duration  float64 — shortest scene duration, default 0.8s
type DetectScenesExecutor struct{}

type Scene struct {
	Index      int     `json:"index"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Duration   float64 `json:"duration"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

type SceneWarning struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

var ptsTimePattern = regexp.MustCompile(`pts_time:([0-9]+(?:\.[0-9]+)?)`)

func (e *DetectScenesExecutor) Run(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, error) {
	outputPath, outputPaths, _, err := e.RunData(ctx, params, jobID, cfg)
	return outputPath, outputPaths, err
}

func (e *DetectScenesExecutor) RunData(ctx context.Context, params map[string]any, jobID string, cfg Config) (string, []string, map[string]any, error) {
	videoPath, ok := getString(params, "video_path")
	if !ok || videoPath == "" {
		return "", nil, nil, fmt.Errorf("detect-scenes: video_path required")
	}

	threshold := 0.3
	if v, ok := getFloat64(params, "threshold"); ok && v > 0 {
		threshold = v
	}
	minSceneDuration := 0.8
	if v, ok := getFloat64(params, "min_scene_duration"); ok && v > 0 {
		minSceneDuration = v
	}

	probe, err := Probe(ctx, cfg.FFprobePath, videoPath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("detect-scenes probe: %w", err)
	}
	duration := roundSec(probe.DurationSec)
	if duration <= 0 {
		return "", nil, nil, fmt.Errorf("detect-scenes: invalid video duration %.3f", probe.DurationSec)
	}

	args := []string{
		"-hide_banner",
		"-i", videoPath,
		"-filter:v", fmt.Sprintf("select='gt(scene,%.6f)',showinfo", threshold),
		"-f", "null",
		"-",
	}
	cmd := exec.CommandContext(ctx, cfg.FFmpegPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, nil, fmt.Errorf("detect-scenes ffmpeg: %w; output: %s", err, string(out))
	}

	boundaries := parseSceneBoundaries(string(out), duration)
	warnings := make([]SceneWarning, 0)
	source := "ffmpeg_scene"
	confidence := threshold
	if len(boundaries) == 0 {
		source = "fallback_full"
		confidence = 0.5
		warnings = append(warnings, SceneWarning{
			Type:    "no_scene_boundary",
			Message: "no scene boundary detected, fallback to full video",
		})
	}

	scenes := buildScenes(duration, threshold, minSceneDuration, boundaries, source, confidence)
	outputData := map[string]any{
		"duration":           duration,
		"threshold":          threshold,
		"min_scene_duration": minSceneDuration,
		"scenes":             scenes,
		"warnings":           warnings,
	}
	return "", nil, outputData, nil
}

func parseSceneBoundaries(output string, duration float64) []float64 {
	matches := ptsTimePattern.FindAllStringSubmatch(output, -1)
	values := make([]float64, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			continue
		}
		if v <= 0.001 || (duration > 0 && v >= duration-0.001) {
			continue
		}
		values = append(values, roundSec(v))
	}
	return uniqueSortedFloat64(values, 0.001)
}

func buildScenes(duration, threshold, minSceneDuration float64, boundaries []float64, source string, confidence float64) []Scene {
	points := make([]float64, 0, len(boundaries)+2)
	points = append(points, 0)
	points = append(points, boundaries...)
	points = append(points, duration)
	points = uniqueSortedFloat64(points, 0.001)

	if len(points) < 2 {
		points = []float64{0, duration}
	}

	scenes := make([]Scene, 0, len(points)-1)
	for i := 0; i < len(points)-1; i++ {
		start := roundSec(points[i])
		end := roundSec(points[i+1])
		if end <= start {
			continue
		}
		scenes = append(scenes, Scene{
			Start:      start,
			End:        end,
			Duration:   roundSec(end - start),
			Source:     source,
			Confidence: confidence,
		})
	}
	if len(scenes) == 0 {
		scenes = append(scenes, Scene{Start: 0, End: duration, Duration: duration, Source: source, Confidence: confidence})
	}

	scenes = mergeShortScenes(scenes, minSceneDuration)
	return reindexScenes(scenes)
}

func mergeShortScenes(scenes []Scene, minDuration float64) []Scene {
	if minDuration <= 0 || len(scenes) <= 1 {
		return scenes
	}

	merged := make([]Scene, 0, len(scenes))
	for _, scene := range scenes {
		scene.Duration = roundSec(scene.End - scene.Start)
		if scene.Duration >= minDuration {
			merged = append(merged, scene)
			continue
		}

		if len(merged) > 0 {
			prev := &merged[len(merged)-1]
			prev.End = scene.End
			prev.Duration = roundSec(prev.End - prev.Start)
			continue
		}

		if len(scenes) > 1 {
			merged = append(merged, scene)
			continue
		}
		merged = append(merged, scene)
	}

	if len(merged) > 1 && merged[0].Duration < minDuration {
		merged[1].Start = merged[0].Start
		merged[1].Duration = roundSec(merged[1].End - merged[1].Start)
		merged = merged[1:]
	}
	return merged
}

func reindexScenes(scenes []Scene) []Scene {
	for i := range scenes {
		scenes[i].Index = i + 1
		scenes[i].Start = roundSec(scenes[i].Start)
		scenes[i].End = roundSec(scenes[i].End)
		scenes[i].Duration = roundSec(scenes[i].End - scenes[i].Start)
	}
	return scenes
}

func uniqueSortedFloat64(values []float64, epsilon float64) []float64 {
	if len(values) == 0 {
		return values
	}
	sort.Float64s(values)
	out := make([]float64, 0, len(values))
	for _, v := range values {
		if len(out) == 0 || math.Abs(v-out[len(out)-1]) > epsilon {
			out = append(out, v)
		}
	}
	return out
}

func roundSec(v float64) float64 {
	return math.Round(v*1000) / 1000
}
