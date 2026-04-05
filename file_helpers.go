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

// flush writes the file's buffer content to GitHub using the Contents API.
// It creates a new file if the SHA is not in the cache, or updates an existing file if the SHA is present.
// After a successful flush, it updates the SHA cache with the response and refreshes the MemMapFs layer.
// The dirty flag is cleared on success.
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

// getReader returns the underlying bytes.Reader. If the reader is not yet initialized
// (e.g., the file was opened for write-only), it creates one from the buffer content.
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

// currentSize returns the current file size. If the file is dirty or has no reader,
// it returns the buffer size. Otherwise, it returns the reader size.
func (f *File) currentSize() int64 {
	if f.dirty || f.reader == nil {
		return int64(f.buf.Len())
	}
	return f.reader.Size()
}

// rawContentURL constructs the raw.githubusercontent.com URL for the file.
// This URL is used for HTTP Range requests to efficiently fetch byte ranges.
// Returns an error if the required GitHub configuration is incomplete.
func (f *File) rawContentURL() (string, error) {
	if f.fs.Owner() == "" || f.fs.Repo() == "" || f.fs.Branch() == "" || f.path == "" {
		return "", errors.New("incomplete GitHubFs configuration")
	}
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/%s/%s",
		f.fs.Owner(), f.fs.Repo(), f.fs.Branch(), f.path,
	), nil
}

// newFileForWrite creates a File prepared for writing. If O_APPEND or O_RDWR is set,
// it pre-loads the existing file content so that the buffer reflects the correct base
// for subsequent flush operations (GitHub always replaces the entire blob).
// Returns an error if fetching existing content fails (unless ErrNotExist for new files).
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

// newDir creates a File representing a directory with preloaded entries.
// The dirEntries slice is used for subsequent Readdir calls.
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

// contextCloser wraps an io.ReadCloser and cancels an associated context when closed.
// This ensures that long-running HTTP operations are properly cleaned up.
type contextCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close cancels the associated context and closes the underlying ReadCloser.
// The context is cancelled first (via defer) to ensure cleanup happens regardless of the close error.
func (c *contextCloser) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}
