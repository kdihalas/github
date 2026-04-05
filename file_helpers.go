package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/go-github/v84/github"
)

// flush writes the current buffer content to GitHub using the Contents API. It handles both creating new files and
// updating existing ones based on the presence of a SHA in the cache. After a successful flush, it updates the SHA
// cache and refreshes the in-memory file system layer to reflect the latest content. It returns any error encountered during the process.
func (f *File) flush() error {
	sha := f.fs.getSHA(f.path)
	data := f.buf.Bytes()

	message := "chore: update " + f.path
	opts := &github.RepositoryContentFileOptions{
		Message: &message,
		Content: data,
		Branch:  f.fs.branch,
	}
	if sha != "" {
		opts.SHA = &sha
	}

	ctx, cancel := f.fs.newContext()
	defer cancel()

	resp, _, err := f.fs.github().Repositories.UpdateFile(
		ctx,
		f.fs.Owner(),
		f.fs.Repo(),
		f.path,
		opts,
	)
	if err != nil {
		return err
	}

	if resp.Content != nil && resp.Content.SHA != nil {
		f.fs.setSHA(f.path, *resp.Content.SHA)
		// Also refresh the MemMapFs layer on successful write.
		_ = f.fs.memFs.MkdirAll(filepath.Dir(f.path), 0755)
		_ = writeMemFile(f.fs.memFs, f.path, data)
	}

	f.dirty = false
	return nil
}

// getReader returns the underlying bytes.Reader, building it from the write
// buffer when the file was opened for RDWR.
func (f *File) getReader() *bytes.Reader {
	if f.reader == nil {
		f.reader = bytes.NewReader(f.buf.Bytes())
	}
	return f.reader
}

// readable returns true if the file was opened with read permissions (O_RDONLY or O_RDWR).
func (f *File) readable() bool {
	return f.flag == os.O_RDONLY || f.flag&os.O_RDWR != 0
}

// writable returns true if the file was opened with write permissions (O_WRONLY, O_RDWR, or O_CREATE).
func (f *File) writable() bool {
	return f.flag&os.O_WRONLY != 0 || f.flag&os.O_RDWR != 0 || f.flag&os.O_CREATE != 0
}

// currentSize returns the current size of the file content. If the file is dirty or has no reader,
// it returns the size of the write buffer. Otherwise, it returns the size of the reader's content.
func (f *File) currentSize() int64 {
	if f.dirty || f.reader == nil {
		return int64(f.buf.Len())
	}
	return f.reader.Size()
}

// rawContentURL constructs the raw content URL for the file based on the GitHub repository information and file path.
// It returns an error if any of the required configuration (owner, repo, branch, or path) is missing.
func (f *File) rawContentURL() (string, error) {
	if f.fs.Owner() == "" || f.fs.Repo() == "" || f.fs.Branch() == "" || f.path == "" {
		return "", errors.New("incomplete GitHubFs configuration")
	}
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/%s/%s",
		f.fs.Owner(), f.fs.Repo(), f.fs.Branch(), f.path,
	), nil
}

// newFileForWrite prepares a write-mode file, pre-loading current content
// if O_APPEND or O_RDWR so we hold the right base for the flush.
func newFileForWrite(fs *Fs, path string, flag int) (*File, error) {
	f := &File{
		fs:    fs,
		path:  path,
		name:  filepath.Base(path),
		buf:   new(bytes.Buffer),
		flag:  flag,
		isDir: false,
	}

	isAppend := flag&os.O_APPEND != 0
	isRdWr := flag&os.O_RDWR != 0

	if isAppend || isRdWr {
		existing, err := fs.getContent(path)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		if len(existing) > 0 {
			f.buf.Write(existing)
			f.reader = bytes.NewReader(existing)
		}
	}

	return f, nil
}

// newDir creates a new File instance representing a directory, with the provided entries.
func newDir(fs *Fs, path string, entries []os.FileInfo) *File {
	return &File{
		fs:         fs,
		path:       path,
		name:       filepath.Base(path),
		buf:        new(bytes.Buffer),
		isDir:      true,
		dirEntries: entries,
	}
}

// contextCloser cancels the request context when the body is closed.
type contextCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close implements the io.Closer interface for contextCloser. It cancels the associated context
// and then closes the underlying ReadCloser. This ensures that any ongoing operations using the
// context are properly terminated when the reader is closed.
func (c *contextCloser) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}
