package github

import (
	"os"
	"time"
)

// GitHubFileInfo implements os.FileInfo.
type GitHubFileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
	mode    os.FileMode
}

func (fi *GitHubFileInfo) Name() string       { return fi.name }
func (fi *GitHubFileInfo) Size() int64        { return fi.size }
func (fi *GitHubFileInfo) IsDir() bool        { return fi.isDir }
func (fi *GitHubFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *GitHubFileInfo) Sys() any           { return nil }
func (fi *GitHubFileInfo) Mode() os.FileMode {
	if fi.isDir {
		return fi.mode | os.ModeDir
	}
	return fi.mode
}
