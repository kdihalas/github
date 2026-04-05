package github

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
)

// File implements afero.File and represents a file or directory in the GitHub repository.
// For regular files, it maintains separate read (bytes.Reader) and write (bytes.Buffer) state
// to support efficient operations while avoiding GitHub's blob-replacement semantics.
// For directories, it holds preloaded entries and an iteration offset.
type File struct {
	fs   *Fs // Reference to the file system this file belongs to.
	path string
	name string // base name only

	// read state
	reader *bytes.Reader

	// write state
	buf   *bytes.Buffer
	dirty bool

	// metadata
	flag  int
	isDir bool

	// directory iteration state
	dirEntries []os.FileInfo
	dirOffset  int

	closed bool
}

// NewFile creates a new File instance with the provided Fs, path, and name.
// The file is not opened for reading or writing until Read or Write is called.
func NewFile(fs *Fs, path, name string) *File {
	return &File{
		fs:   fs,
		path: path,
		name: name,
	}
}

// Name returns the base name of the file.
func (f *File) Name() string { return f.name }

// Readdir reads the next count directory entries and returns them as a slice of os.FileInfo.
// It maintains an internal offset to support multiple calls.
// If count <= 0, it returns all remaining entries.
// Returns io.EOF when all entries have been read.
// Returns an error if the file is not a directory or is closed.
func (f *File) Readdir(count int) ([]os.FileInfo, error) {
	if f.closed {
		return nil, os.ErrClosed
	}
	if !f.isDir {
		return nil, &os.PathError{Op: "readdir", Path: f.path, Err: errors.New("not a directory")}
	}

	remaining := f.dirEntries[f.dirOffset:]

	if count <= 0 {
		f.dirOffset = len(f.dirEntries)
		return remaining, nil
	}

	if len(remaining) == 0 {
		return nil, io.EOF
	}

	if count > len(remaining) {
		count = len(remaining)
	}

	entries := remaining[:count]
	f.dirOffset += count
	return entries, nil
}

// ReaddirAll returns all directory entries and resets the internal offset for subsequent reads.
// Returns an error if the file is not a directory or is closed.
func (f *File) ReaddirAll() ([]os.FileInfo, error) {
	if f.closed {
		return nil, os.ErrClosed
	}
	if !f.isDir {
		return nil, &os.PathError{
			Op:   "readdirall",
			Path: f.path,
			Err:  errors.New("not a directory"),
		}
	}

	// Reset cursor so we always return the full listing.
	f.dirOffset = 0
	return f.Readdir(-1)
}

// Readdirnames returns a slice of the names of the next count directory entries.
// Returns an error if the file is not a directory or is closed.
func (f *File) Readdirnames(count int) ([]string, error) {
	infos, err := f.Readdir(count)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name()
	}
	return names, nil
}

// Stat returns a FileInfo describing the file.
// It returns the current size from either the reader (if readable) or buffer (if writable).
// Returns an error if the file is closed.
func (f *File) Stat() (os.FileInfo, error) {
	if f.closed {
		return nil, os.ErrClosed
	}
	return &GitHubFileInfo{
		name:  f.name,
		size:  f.currentSize(),
		isDir: f.isDir,
	}, nil
}

// Sync flushes any pending writes to GitHub and clears the dirty flag.
// Returns an error if the file is closed or the flush operation fails.
func (f *File) Sync() error {
	if f.closed {
		return os.ErrClosed
	}
	if f.dirty {
		return f.flush()
	}
	return nil
}

// Truncate changes the file size to the given size.
// If size is greater than the current size, the file is extended with zero bytes.
// If size is less than the current size, the file is truncated.
// Returns an error if the file is closed or not writable.
func (f *File) Truncate(size int64) error {
	if f.closed {
		return os.ErrClosed
	}
	if !f.writable() {
		return &os.PathError{Op: "truncate", Path: f.path, Err: fs.ErrPermission}
	}

	data := f.buf.Bytes()
	if int64(len(data)) >= size {
		data = data[:size]
	} else {
		data = append(data, make([]byte, size-int64(len(data)))...)
	}
	f.buf.Reset()
	f.buf.Write(data)
	f.dirty = true
	return nil
}

// WriteString writes the string s to the file and returns the number of bytes written.
// It marks the file as dirty. Returns an error if the file is closed, is a directory, or not writable.
func (f *File) WriteString(s string) (n int, err error) {
	return f.Write([]byte(s))
}

// Close closes the file. If the file has been modified (dirty), it flushes the changes to GitHub first.
// Returns an error if the file is already closed or if the flush operation fails.
func (f *File) Close() error {
	if f.closed {
		return os.ErrClosed
	}
	f.closed = true

	if f.dirty {
		if err := f.flush(); err != nil {
			return &os.PathError{Op: "close", Path: f.path, Err: err}
		}
	}
	return nil
}

// Read reads up to len(p) bytes from the file and stores them in p.
// It returns the number of bytes read and any error encountered.
// Returns an error if the file is closed, is a directory, or not readable.
func (f *File) Read(p []byte) (n int, err error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if f.isDir {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: errors.New("is a directory")}
	}
	if !f.readable() {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: fs.ErrPermission}
	}

	r := f.getReader()
	return r.Read(p)
}

// ReadAt reads up to len(p) bytes from the file starting at byte offset off
// and stores them in p. It returns the number of bytes read and any error encountered.
// Returns an error if the file is closed or not readable.
func (f *File) ReadAt(p []byte, off int64) (n int, err error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if !f.readable() {
		return 0, &os.PathError{Op: "readat", Path: f.path, Err: fs.ErrPermission}
	}

	r := f.getReader()
	return r.ReadAt(p, off)
}

// Seek changes the read position to the specified offset relative to whence
// (io.SeekStart, io.SeekCurrent, or io.SeekEnd).
// It returns the new absolute offset and any error encountered.
// Returns an error if the file is closed, is a directory, or whence is invalid.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if f.isDir {
		return 0, &os.PathError{Op: "seek", Path: f.path, Err: errors.New("is a directory")}
	}

	// For write-only mode, seek within the write buffer.
	if !f.readable() {
		size := int64(f.buf.Len())
		var abs int64
		switch whence {
		case io.SeekStart:
			abs = offset
		case io.SeekCurrent:
			// We don't track a write cursor; use buf length as proxy.
			abs = size + offset
		case io.SeekEnd:
			abs = size + offset
		default:
			return 0, errors.New("invalid whence")
		}
		if abs < 0 {
			return 0, errors.New("negative position")
		}
		return abs, nil
	}

	return f.getReader().Seek(offset, whence)
}

// Write writes len(p) bytes from p to the file and returns the number of bytes written.
// It marks the file as dirty. Returns an error if the file is closed, is a directory, or not writable.
func (f *File) Write(p []byte) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if f.isDir {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: errors.New("is a directory")}
	}
	if !f.writable() {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: fs.ErrPermission}
	}

	n, err := f.buf.Write(p)
	if n > 0 {
		f.dirty = true
	}
	return n, err
}

// WriteAt writes len(p) bytes from p to the file starting at byte offset off.
// It expands the buffer if necessary and returns the number of bytes written.
// It marks the file as dirty. Returns an error if the file is closed or not writable.
func (f *File) WriteAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if !f.writable() {
		return 0, &os.PathError{Op: "writeat", Path: f.path, Err: fs.ErrPermission}
	}

	// Expand buffer to at least off+len(p).
	data := f.buf.Bytes()
	needed := int(off) + len(p)
	if needed > len(data) {
		extra := make([]byte, needed-len(data))
		data = append(data, extra...)
	}
	copy(data[off:], p)

	f.buf.Reset()
	f.buf.Write(data)
	f.dirty = true

	return len(p), nil
}

// RangeReader returns an io.ReadCloser for reading a specific byte range from the file.
// It first tries to serve the range from the in-memory reader if available and sufficient.
// Otherwise, it issues an HTTP Range request to raw.githubusercontent.com for efficient partial downloads.
// The returned reader's context is cancelled when the reader is closed.
// Returns an error if the file is closed, is a directory, not readable, or range parameters are invalid.
func (f *File) RangeReader(offset, length int64) (io.ReadCloser, error) {
	if f.closed {
		return nil, os.ErrClosed
	}
	if f.isDir {
		return nil, &os.PathError{Op: "rangeread", Path: f.path, Err: errors.New("is a directory")}
	}
	if offset < 0 || length <= 0 {
		return nil, &os.PathError{Op: "rangeread", Path: f.path, Err: errors.New("invalid range")}
	}

	// If the requested range is within the current reader, serve it directly without an HTTP request.
	// This optimizes for recently read content that may still be in memory.
	if f.reader != nil {
		size := f.reader.Size()
		if offset+length <= size {
			buf := make([]byte, length)
			n, err := f.reader.ReadAt(buf, offset)
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(buf[:n])), nil
		}
	}

	// Otherwise, make an HTTP Range request to fetch the specified byte range from GitHub.
	rawURL, err := f.rawContentURL()
	if err != nil {
		return nil, err
	}

	ctx, cancel := f.fs.newContext()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		cancel()
		return nil, &os.PathError{Op: "rangeread", Path: f.path, Err: err}
	}

	// Set the Range header to request the specific byte range. The end byte is inclusive, so we subtract 1 from the length.
	end := offset + length - 1
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))

	resp, err := f.fs.httpClient().Do(req)
	if err != nil {
		cancel()
		return nil, &os.PathError{Op: "rangeread", Path: f.path, Err: err}
	}

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		cancel()
		_ = resp.Body.Close()
		return nil, &os.PathError{
			Op:   "rangeread",
			Path: f.path,
			Err:  fmt.Errorf("unexpected HTTP status %d", resp.StatusCode),
		}
	}

	// Wrap body so that the context is cancelled when the caller closes.
	return &contextCloser{ReadCloser: resp.Body, cancel: cancel}, nil
}
