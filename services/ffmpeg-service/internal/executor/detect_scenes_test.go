package executor

import "testing"

func TestParseSceneBoundaries(t *testing.T) {
	output := `
[Parsed_showinfo_1 @ 0x123] n:   0 pts: 15360 pts_time:3.200 pos: 123
[Parsed_showinfo_1 @ 0x123] n:   1 pts: 32640 pts_time:6.800 pos: 456
[Parsed_showinfo_1 @ 0x123] n:   2 pts: 32641 pts_time:6.800 pos: 789
`
	got := parseSceneBoundaries(output, 10)
	want := []float64{3.2, 6.8}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%v want %v", i, got[i], want[i])
		}
	}
}

func TestBuildScenesFallbackFull(t *testing.T) {
	scenes := buildScenes(85.972, 0.3, 0.8, nil, "fallback_full", 0.5)
	if len(scenes) != 1 {
		t.Fatalf("len=%d want 1", len(scenes))
	}
	if scenes[0].Index != 1 || scenes[0].Start != 0 || scenes[0].End != 85.972 || scenes[0].Duration != 85.972 {
		t.Fatalf("unexpected scene: %+v", scenes[0])
	}
	if scenes[0].Source != "fallback_full" || scenes[0].Confidence != 0.5 {
		t.Fatalf("unexpected source/confidence: %+v", scenes[0])
	}
}

func TestBuildScenesMergesShortScenesAndCoversDuration(t *testing.T) {
	scenes := buildScenes(10, 0.3, 0.8, []float64{0.3, 2, 2.4, 6}, "ffmpeg_scene", 0.3)
	if len(scenes) != 3 {
		t.Fatalf("len=%d want 3 scenes=%+v", len(scenes), scenes)
	}
	if scenes[0].Start != 0 || scenes[len(scenes)-1].End != 10 {
		t.Fatalf("scenes do not cover duration: %+v", scenes)
	}
	for i := 1; i < len(scenes); i++ {
		if scenes[i-1].End != scenes[i].Start {
			t.Fatalf("gap/overlap between scenes: %+v", scenes)
		}
	}
	for _, scene := range scenes {
		if scene.Duration <= 0 {
			t.Fatalf("non-positive scene duration: %+v", scene)
		}
	}
}
