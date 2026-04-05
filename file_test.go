package github

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v84/github"
	"github.com/spf13/afero"
)

// ---------- helpers ----------

func makeFile(t *testing.T, content string, flag int) *File {
	t.Helper()
	f := &File{
		path: "test/file.txt",
		name: "file.txt",
		buf:  newBufferFrom([]byte(content)),
		flag: flag,
	}
	if flag == os.O_RDONLY || flag&os.O_RDWR != 0 {
		f.reader = newReaderFrom([]byte(content))
	}
	return f
}

func makeDir(t *testing.T, entries []os.FileInfo) *File {
	t.Helper()
	return &File{
		path:       "test/dir",
		name:       "dir",
		buf:        new(bytes.Buffer),
		isDir:      true,
		dirEntries: entries,
	}
}

func makeFileInfo(name string, isDir bool) *GitHubFileInfo {
	return &GitHubFileInfo{name: name, isDir: isDir}
}

// ---------- NewFile ----------

func TestNewFile(t *testing.T) {
	testFs, _, _ := newTestFs(t, "owner", "repo", "main")
	f := NewFile(testFs, "path/file.txt", "file.txt")
	if f.Name() != "file.txt" {
		t.Errorf("Name() = %q", f.Name())
	}
}

// ---------- Name ----------

func TestFile_Name(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	if f.Name() != "file.txt" {
		t.Errorf("Name() = %q", f.Name())
	}
}

// ---------- Stat ----------

func TestFile_Stat(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Name() != "file.txt" {
		t.Errorf("Stat().Name() = %q", fi.Name())
	}
}

func TestFile_Stat_Closed(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	f.closed = true
	_, err := f.Stat()
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

// ---------- Read ----------

func TestFile_Read(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	buf := make([]byte, 5)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read() error: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("Read() = %q", buf[:n])
	}
}

func TestFile_Read_Closed(t *testing.T) {
	f := makeFile(t, "x", os.O_RDONLY)
	f.closed = true
	_, err := f.Read(make([]byte, 1))
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_Read_IsDir(t *testing.T) {
	f := makeDir(t, nil)
	_, err := f.Read(make([]byte, 1))
	if err == nil {
		t.Error("expected error reading directory")
	}
}

func TestFile_Read_NotReadable(t *testing.T) {
	// O_WRONLY only = not readable
	f := makeFile(t, "", os.O_WRONLY)
	_, err := f.Read(make([]byte, 1))
	if err == nil {
		t.Error("expected permission error")
	}
}

// ---------- ReadAt ----------

func TestFile_ReadAt(t *testing.T) {
	f := makeFile(t, "hello world", os.O_RDONLY)
	buf := make([]byte, 5)
	n, _ := f.ReadAt(buf, 6)
	if string(buf[:n]) != "world" {
		t.Errorf("ReadAt() = %q", buf[:n])
	}
}

func TestFile_ReadAt_Closed(t *testing.T) {
	f := makeFile(t, "x", os.O_RDONLY)
	f.closed = true
	_, err := f.ReadAt(make([]byte, 1), 0)
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_ReadAt_NotReadable(t *testing.T) {
	f := makeFile(t, "x", os.O_WRONLY)
	_, err := f.ReadAt(make([]byte, 1), 0)
	if err == nil {
		t.Error("expected permission error")
	}
}

// ---------- Seek ----------

func TestFile_Seek_SeekStart(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	pos, err := f.Seek(2, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek() error: %v", err)
	}
	if pos != 2 {
		t.Errorf("Seek() = %d, want 2", pos)
	}
}

func TestFile_Seek_SeekCurrent(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	f.Seek(2, io.SeekStart)
	pos, err := f.Seek(1, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek() error: %v", err)
	}
	if pos != 3 {
		t.Errorf("Seek() = %d, want 3", pos)
	}
}

func TestFile_Seek_SeekEnd(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	pos, err := f.Seek(-1, io.SeekEnd)
	if err != nil {
		t.Fatalf("Seek() error: %v", err)
	}
	if pos != 4 {
		t.Errorf("Seek() = %d, want 4", pos)
	}
}

func TestFile_Seek_Closed(t *testing.T) {
	f := makeFile(t, "x", os.O_RDONLY)
	f.closed = true
	_, err := f.Seek(0, io.SeekStart)
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_Seek_IsDir(t *testing.T) {
	f := makeDir(t, nil)
	_, err := f.Seek(0, io.SeekStart)
	if err == nil {
		t.Error("expected error seeking directory")
	}
}

func TestFile_Seek_WriteOnly_SeekStart(t *testing.T) {
	f := makeFile(t, "hello", os.O_WRONLY)
	pos, err := f.Seek(3, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek() error: %v", err)
	}
	if pos != 3 {
		t.Errorf("Seek() = %d, want 3", pos)
	}
}

func TestFile_Seek_WriteOnly_SeekCurrent(t *testing.T) {
	f := makeFile(t, "hello", os.O_WRONLY)
	pos, err := f.Seek(1, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek() error: %v", err)
	}
	// buf.Len() = 5, +1 = 6
	if pos != 6 {
		t.Errorf("Seek() = %d, want 6", pos)
	}
}

func TestFile_Seek_WriteOnly_SeekEnd(t *testing.T) {
	f := makeFile(t, "hello", os.O_WRONLY)
	pos, err := f.Seek(-1, io.SeekEnd)
	if err != nil {
		t.Fatalf("Seek() error: %v", err)
	}
	// buf.Len() = 5, -1 = 4
	if pos != 4 {
		t.Errorf("Seek() = %d, want 4", pos)
	}
}

func TestFile_Seek_WriteOnly_InvalidWhence(t *testing.T) {
	f := makeFile(t, "", os.O_WRONLY)
	_, err := f.Seek(0, 99)
	if err == nil {
		t.Error("expected error for invalid whence")
	}
}

func TestFile_Seek_WriteOnly_NegativePosition(t *testing.T) {
	f := makeFile(t, "", os.O_WRONLY)
	_, err := f.Seek(-1, io.SeekStart)
	if err == nil {
		t.Error("expected error for negative position")
	}
}

// ---------- Write ----------

func TestFile_Write(t *testing.T) {
	f := makeFile(t, "", os.O_WRONLY)
	n, err := f.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write() n = %d, want 5", n)
	}
	if !f.dirty {
		t.Error("expected dirty=true")
	}
}

func TestFile_Write_Closed(t *testing.T) {
	f := makeFile(t, "", os.O_WRONLY)
	f.closed = true
	_, err := f.Write([]byte("x"))
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_Write_IsDir(t *testing.T) {
	f := makeDir(t, nil)
	_, err := f.Write([]byte("x"))
	if err == nil {
		t.Error("expected error writing to directory")
	}
}

func TestFile_Write_NotWritable(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	_, err := f.Write([]byte("x"))
	if err == nil {
		t.Error("expected permission error")
	}
}

// ---------- WriteAt ----------

func TestFile_WriteAt(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDWR)
	n, err := f.WriteAt([]byte("world"), 6)
	if err != nil {
		t.Fatalf("WriteAt() error: %v", err)
	}
	if n != 5 {
		t.Errorf("WriteAt() n = %d, want 5", n)
	}
}

func TestFile_WriteAt_Closed(t *testing.T) {
	f := makeFile(t, "", os.O_WRONLY)
	f.closed = true
	_, err := f.WriteAt([]byte("x"), 0)
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_WriteAt_NotWritable(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	_, err := f.WriteAt([]byte("x"), 0)
	if err == nil {
		t.Error("expected permission error")
	}
}

func TestFile_WriteAt_InBounds(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDWR)
	n, err := f.WriteAt([]byte("X"), 1)
	if err != nil {
		t.Fatalf("WriteAt() error: %v", err)
	}
	if n != 1 {
		t.Errorf("WriteAt() n = %d, want 1", n)
	}
	if f.buf.String() != "hXllo" {
		t.Errorf("WriteAt() content = %q", f.buf.String())
	}
}

// ---------- WriteString ----------

func TestFile_WriteString(t *testing.T) {
	f := makeFile(t, "", os.O_WRONLY)
	n, err := f.WriteString("hello")
	if err != nil {
		t.Fatalf("WriteString() error: %v", err)
	}
	if n != 5 {
		t.Errorf("WriteString() n = %d", n)
	}
}

// ---------- Truncate ----------

func TestFile_Truncate_Shrink(t *testing.T) {
	f := makeFile(t, "hello world", os.O_RDWR)
	if err := f.Truncate(5); err != nil {
		t.Fatalf("Truncate() error: %v", err)
	}
	if f.buf.String() != "hello" {
		t.Errorf("Truncate() content = %q", f.buf.String())
	}
}

func TestFile_Truncate_Grow(t *testing.T) {
	f := makeFile(t, "hi", os.O_RDWR)
	if err := f.Truncate(5); err != nil {
		t.Fatalf("Truncate() error: %v", err)
	}
	if f.buf.Len() != 5 {
		t.Errorf("Truncate() len = %d, want 5", f.buf.Len())
	}
}

func TestFile_Truncate_Closed(t *testing.T) {
	f := makeFile(t, "", os.O_RDWR)
	f.closed = true
	if err := f.Truncate(0); err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_Truncate_NotWritable(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	err := f.Truncate(0)
	if err == nil {
		t.Error("expected permission error")
	}
	var perr *os.PathError
	if !isPathError(err, &perr) {
		t.Errorf("expected PathError, got %T", err)
	}
	if perr.Err != fs.ErrPermission {
		t.Errorf("expected ErrPermission, got %v", perr.Err)
	}
}

func isPathError(err error, target **os.PathError) bool {
	if e, ok := err.(*os.PathError); ok {
		*target = e
		return true
	}
	return false
}

// ---------- Sync ----------

func TestFile_Sync_Clean(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
}

func TestFile_Sync_Closed(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	f.closed = true
	if err := f.Sync(); err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

// ---------- Close ----------

func TestFile_Close_AlreadyClosed(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	f.closed = true
	if err := f.Close(); err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_Close_Clean(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if !f.closed {
		t.Error("expected closed=true")
	}
}

// ---------- Readdir ----------

func TestFile_Readdir_NotDir(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	_, err := f.Readdir(0)
	if err == nil {
		t.Error("expected error for non-dir")
	}
}

func TestFile_Readdir_Closed(t *testing.T) {
	f := makeDir(t, nil)
	f.closed = true
	_, err := f.Readdir(0)
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_Readdir_All(t *testing.T) {
	entries := []os.FileInfo{makeFileInfo("a.txt", false), makeFileInfo("b.txt", false)}
	f := makeDir(t, entries)
	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir(-1) error: %v", err)
	}
	if len(infos) != 2 {
		t.Errorf("Readdir(-1) len = %d, want 2", len(infos))
	}
}

func TestFile_Readdir_Count(t *testing.T) {
	entries := []os.FileInfo{makeFileInfo("a.txt", false), makeFileInfo("b.txt", false), makeFileInfo("c.txt", false)}
	f := makeDir(t, entries)

	infos, err := f.Readdir(2)
	if err != nil {
		t.Fatalf("Readdir(2) error: %v", err)
	}
	if len(infos) != 2 {
		t.Errorf("Readdir(2) len = %d, want 2", len(infos))
	}

	// Read remaining
	infos, err = f.Readdir(2)
	if err != nil {
		t.Fatalf("second Readdir error: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("second Readdir len = %d, want 1", len(infos))
	}
}

func TestFile_Readdir_EOF(t *testing.T) {
	f := makeDir(t, nil)
	_, err := f.Readdir(1)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestFile_Readdir_CountGtRemaining(t *testing.T) {
	entries := []os.FileInfo{makeFileInfo("a.txt", false)}
	f := makeDir(t, entries)
	infos, err := f.Readdir(5)
	if err != nil {
		t.Fatalf("Readdir(5) error: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("Readdir(5) len = %d, want 1", len(infos))
	}
}

// ---------- ReaddirAll ----------

func TestFile_ReaddirAll(t *testing.T) {
	entries := []os.FileInfo{makeFileInfo("x.txt", false)}
	f := makeDir(t, entries)
	infos, err := f.ReaddirAll()
	if err != nil {
		t.Fatalf("ReaddirAll() error: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("ReaddirAll() len = %d, want 1", len(infos))
	}
}

func TestFile_ReaddirAll_Closed(t *testing.T) {
	f := makeDir(t, nil)
	f.closed = true
	_, err := f.ReaddirAll()
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_ReaddirAll_NotDir(t *testing.T) {
	f := makeFile(t, "", os.O_RDONLY)
	_, err := f.ReaddirAll()
	if err == nil {
		t.Error("expected error for non-dir")
	}
}

// ---------- Readdirnames ----------

func TestFile_Readdirnames(t *testing.T) {
	entries := []os.FileInfo{makeFileInfo("a.txt", false), makeFileInfo("b.txt", false)}
	f := makeDir(t, entries)
	names, err := f.Readdirnames(-1)
	if err != nil {
		t.Fatalf("Readdirnames() error: %v", err)
	}
	if len(names) != 2 || names[0] != "a.txt" {
		t.Errorf("Readdirnames() = %v", names)
	}
}

// ---------- RangeReader (in-memory path) ----------

func TestFile_RangeReader_InMemory(t *testing.T) {
	f := makeFile(t, "hello world", os.O_RDONLY)
	rc, err := f.RangeReader(0, 5)
	if err != nil {
		t.Fatalf("RangeReader() error: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello" {
		t.Errorf("RangeReader() = %q, want %q", data, "hello")
	}
}

func TestFile_RangeReader_Closed(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	f.closed = true
	_, err := f.RangeReader(0, 5)
	if err != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got %v", err)
	}
}

func TestFile_RangeReader_IsDir(t *testing.T) {
	f := makeDir(t, nil)
	_, err := f.RangeReader(0, 5)
	if err == nil {
		t.Error("expected error for directory")
	}
}

func TestFile_RangeReader_InvalidRange_NegOffset(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	_, err := f.RangeReader(-1, 5)
	if err == nil {
		t.Error("expected error for negative offset")
	}
}

func TestFile_RangeReader_InvalidRange_ZeroLength(t *testing.T) {
	f := makeFile(t, "hello", os.O_RDONLY)
	_, err := f.RangeReader(0, 0)
	if err == nil {
		t.Error("expected error for zero length")
	}
}

func TestFile_RangeReader_HTTPPath(t *testing.T) {
	// RangeReader falls through to HTTP when offset+length > reader size.
	// Build a gh.Client that wraps a custom transport so httpClient() returns
	// an http.Client whose transport we control.
	transport := &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Body:       io.NopCloser(strings.NewReader("hello")),
		}, nil
	}}
	ghClient := gh.NewClient(&http.Client{Transport: transport})
	client := &Client{ctx: context.Background(), client: ghClient}

	owner, repo, branch := "owner", "repo", "main"
	testFs := NewFsFromClient(client, owner, repo, branch)

	// Build a file with no reader so the HTTP path is taken.
	f := &File{
		fs:   testFs,
		path: "docs/file.txt",
		name: "file.txt",
		buf:  newBufferFrom(nil),
		flag: os.O_RDONLY,
	}

	rc, err := f.RangeReader(0, 5)
	if err != nil {
		t.Fatalf("RangeReader() error: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello" {
		t.Errorf("RangeReader() = %q, want %q", data, "hello")
	}
}

func TestFile_RangeReader_HTTPPath_DoError(t *testing.T) {
	// Exercises file.go:334-337: httpClient().Do(req) returns a transport-level error.
	transport := &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("transport error")
	}}
	ghClient := gh.NewClient(&http.Client{Transport: transport})
	client := &Client{ctx: context.Background(), client: ghClient}
	testFs := NewFsFromClient(client, "owner", "repo", "main")

	f := &File{
		fs:   testFs,
		path: "docs/file.txt",
		name: "file.txt",
		buf:  newBufferFrom(nil),
		flag: os.O_RDONLY,
	}

	_, err := f.RangeReader(0, 5)
	if err == nil {
		t.Error("expected error when HTTP transport fails")
	}
}

func TestFile_RangeReader_HTTPPath_ErrorResponse(t *testing.T) {
	// Exercises the "unexpected HTTP status" branch via a custom transport.
	transport := &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}}
	ghClient := gh.NewClient(&http.Client{Transport: transport})
	client := &Client{ctx: context.Background(), client: ghClient}
	testFs := NewFsFromClient(client, "owner", "repo", "main")

	f := &File{
		fs:   testFs,
		path: "docs/file.txt",
		name: "file.txt",
		buf:  newBufferFrom(nil),
		flag: os.O_RDONLY,
	}

	_, err := f.RangeReader(0, 5)
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
}

func TestFile_RangeReader_RawURLError(t *testing.T) {
	// Exercises rawContentURL returning error when config is incomplete.
	// Build an Fs with empty owner so rawContentURL returns an error.
	fsys := &Fs{
		client:     &Client{ctx: context.Background(), client: gh.NewClient(nil)},
		owner:      strPtr(""),
		repo:       strPtr("repo"),
		branch:     strPtr("main"),
		memFs:      afero.NewMemMapFs(),
		shaCache:   make(map[string]string),
		ttlCache:   make(map[string]time.Time),
		cacheTTL:   defaultCacheTTL,
		apiTimeout: defaultAPITimeout,
	}
	f := &File{
		fs:   fsys,
		path: "file.txt",
		name: "file.txt",
		buf:  newBufferFrom(nil),
		flag: os.O_RDONLY,
		// reader is nil so HTTP path is taken; but rawContentURL will fail.
	}
	_, err := f.RangeReader(0, 5)
	if err == nil {
		t.Error("expected error for incomplete Fs config in RangeReader")
	}
}

func TestFile_Close_DirtyFlushError(t *testing.T) {
	// Exercises the Close → flush → error → PathError branch.
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/dirty.txt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			respondError(w, http.StatusInternalServerError, "flush fail")
		}
	})
	f, err := fsys.OpenFile("dirty.txt", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile error: %v", err)
	}
	gf := f.(*File)
	gf.Write([]byte("data")) // makes dirty=true
	closeErr := gf.Close()
	if closeErr == nil {
		t.Error("expected error from Close when flush fails")
	}
}

func TestFile_Readdirnames_Error(t *testing.T) {
	// Exercises the Readdirnames error-return branch (when Readdir fails).
	f := makeFile(t, "", os.O_RDONLY)
	// Readdirnames calls Readdir which checks isDir; since isDir=false it returns an error.
	_, err := f.Readdirnames(1)
	if err == nil {
		t.Error("expected error from Readdirnames on non-dir file")
	}
}

func TestFile_Stat_ReaderPresent(t *testing.T) {
	// currentSize: dirty=false and reader != nil → returns reader.Size().
	f := makeFile(t, "hello world", os.O_RDONLY)
	// reader is set and dirty is false.
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Size() != 11 {
		t.Errorf("Stat().Size() = %d, want 11", fi.Size())
	}
}

func TestFile_Stat_DirtyFile(t *testing.T) {
	// currentSize: dirty=true → returns buf.Len() even when reader is set.
	f := makeFile(t, "hello", os.O_RDWR)
	f.dirty = true
	f.buf.Write([]byte(" world")) // buf now has "hello world" (11 bytes)
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Size() != 11 {
		t.Errorf("Stat().Size() dirty = %d, want 11", fi.Size())
	}
}

type roundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (rt *roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return rt.fn(r)
}
