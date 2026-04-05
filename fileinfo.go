package github

import (
	"os"
	"time"
)

// GitHubFileInfo implements os.FileInfo for files and directories in a GitHub repository.
type GitHubFileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
	mode    os.FileMode
}

// Name returns the base name of the file.
func (fi *GitHubFileInfo) Name() string { return fi.name }

// Size returns the file size in bytes.
func (fi *GitHubFileInfo) Size() int64 { return fi.size }

// IsDir reports whether the path is a directory.
func (fi *GitHubFileInfo) IsDir() bool { return fi.isDir }

// ModTime returns the modification time. This is currently a zero value as GitHub does not expose file modification times.
func (fi *GitHubFileInfo) ModTime() time.Time { return fi.modTime }

// Sys returns underlying system data. For GitHub files, this is always nil.
func (fi *GitHubFileInfo) Sys() any { return nil }

// Mode returns the file mode bits. For directories, it includes os.ModeDir.
func (fi *GitHubFileInfo) Mode() os.FileMode {
	if fi.isDir {
		return fi.mode | os.ModeDir
	}
	return fi.mode
}
