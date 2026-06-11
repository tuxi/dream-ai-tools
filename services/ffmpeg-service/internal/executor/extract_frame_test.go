package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractFrameOutputArgsForJPEG(t *testing.T) {
	got := extractFrameOutputArgs("jpg")
	want := []string{"-pix_fmt", "yuvj420p", "-threads", "1"}
	if len(got) != len(want) {
		t.Fatalf("len(extractFrameOutputArgs) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extractFrameOutputArgs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestValidateFrameOutputRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jpg")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateFrameOutput(path); err == nil {
		t.Fatal("validateFrameOutput() error = nil, want non-nil")
	}
}

func TestValidateFrameOutputAcceptsNonEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frame.jpg")
	if err := os.WriteFile(path, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateFrameOutput(path); err != nil {
		t.Fatalf("validateFrameOutput() error = %v, want nil", err)
	}
}
