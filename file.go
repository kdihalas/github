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

// File represents a file in the GitHub repository. It contains a reference
// to the Fs it belongs to and the name of the file.
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

// NewFile creates a new File instance with the provided Fs and file name.
func NewFile(fs *Fs, path, name string) *File {
	return &File{
		fs:   fs,
		path: path,
		name: name,
	}
}

// Name returns the name of the file.
func (f *File) Name() string { return f.name }

// Readdir reads the contents of the directory and returns a slice of os.FileInfo for the entries. It maintains an internal
// offset to allow for multiple calls to Readdir, returning subsequent entries until the end of the directory is reached.
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

// ReaddirAll returns all the directory entries. It resets the internal offset to allow for subsequent calls to return the full listing again.
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

// Readdirnames returns a slice of the names of the directory entries. It uses Readdir to get the entries and extracts their names.
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

// Stat returns the FileInfo structure describing the file. It returns an error if the file is closed.
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

// Sync flushes the file's content to GitHub if it has been modified (dirty). It returns an error if the file is closed or if the flush operation fails.
func (f *File) Sync() error {
	if f.closed {
		return os.ErrClosed
	}
	if f.dirty {
		return f.flush()
	}
	return nil
}

// Truncate changes the size of the file to the specified size. If the file is extended, the new bytes are zero-filled.
// It marks the file as dirty if it was modified. It returns an error if the file is closed or if the file is not writable.
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

// WriteString writes the string s to the file. It returns the number of bytes written and any error encountered.
// It marks the file as dirty if it was modified. It returns an error if the file is closed, is a directory, or is not writable.
func (f *File) WriteString(s string) (n int, err error) {
	return f.Write([]byte(s))
}

// Close closes the file, flushing any dirty content to GitHub if necessary. It returns an error if the file is already closed or if the flush operation fails.
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

// Read implements the io.Reader interface for the File. It reads data from the file's reader and returns the number of bytes read and any error encountered.
// It returns an error if the file is closed, is a directory, or is not readable.
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

// ReadAt implements the io.ReaderAt interface for the File. It reads data from the file's reader at the specified offset and returns the number of bytes read and any error encountered.
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

// Seek implements the io.Seeker interface for the File. It changes the read/write position in the file based on the offset and
// whence parameters. It returns the new position and any error encountered. It returns an error if the file is closed, is a directory,
// or if the whence parameter is invalid.
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

// Write implements the io.Writer interface for the File. It writes data to the file's buffer and returns the number of bytes written and any error encountered.
// It marks the file as dirty if it was modified. It returns an error if the file is closed, is a directory, or is not writable.
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

// WriteAt implements the io.WriterAt interface for the File. It writes data to the file's buffer at the specified offset and returns the number of bytes written and any error encountered.
// It marks the file as dirty if it was modified. It returns an error if the file is closed, is a directory, or is not writable.
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

// RangeReader returns an io.ReadCloser that reads a specific byte range from the file. It uses HTTP Range requests against the raw content endpoint to efficiently fetch only the requested portion of the file.
// It returns an error if the file is closed, is a directory, is not readable, or if the range parameters are invalid.
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
