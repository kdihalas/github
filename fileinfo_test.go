package github

import (
	"os"
	"testing"
	"time"
)

func TestGitHubFileInfo(t *testing.T) {
	fi := &GitHubFileInfo{
		name:    "foo.txt",
		size:    42,
		isDir:   false,
		modTime: time.Time{},
		mode:    0644,
	}

	if fi.Name() != "foo.txt" {
		t.Errorf("Name() = %q, want %q", fi.Name(), "foo.txt")
	}
	if fi.Size() != 42 {
		t.Errorf("Size() = %d, want 42", fi.Size())
	}
	if fi.IsDir() {
		t.Error("IsDir() = true, want false")
	}
	if fi.ModTime() != (time.Time{}) {
		t.Errorf("ModTime() unexpected value")
	}
	if fi.Sys() != nil {
		t.Errorf("Sys() = %v, want nil", fi.Sys())
	}
	if fi.Mode()&os.ModeDir != 0 {
		t.Errorf("Mode() should not have ModeDir for file")
	}
	if fi.Mode()&0644 != 0644 {
		t.Errorf("Mode() = %v, expected 0644 bits", fi.Mode())
	}
}

func TestGitHubFileInfo_Dir(t *testing.T) {
	fi := &GitHubFileInfo{
		name:  "mydir",
		isDir: true,
		mode:  0755,
	}

	if !fi.IsDir() {
		t.Error("IsDir() = false, want true")
	}
	if fi.Mode()&os.ModeDir == 0 {
		t.Error("Mode() should have ModeDir for directory")
	}
}
