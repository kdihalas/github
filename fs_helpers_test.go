package github

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/spf13/afero"
)

// ---------- cleanPath ----------

func TestCleanPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/foo/bar", "foo/bar"},
		{"foo/bar", "foo/bar"},
		{"foo//bar", "foo/bar"},
		{"/", ""},
		{"foo/./bar", "foo/bar"},
	}
	for _, c := range cases {
		got := cleanPath(c.in)
		if got != c.want {
			t.Errorf("cleanPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------- newBufferFrom / newReaderFrom ----------

func TestNewBufferFrom(t *testing.T) {
	buf := newBufferFrom([]byte("hello"))
	if buf.String() != "hello" {
		t.Errorf("newBufferFrom content = %q", buf.String())
	}
}

func TestNewReaderFrom(t *testing.T) {
	r := newReaderFrom([]byte("hello"))
	if r.Len() != 5 {
		t.Errorf("newReaderFrom size = %d", r.Len())
	}
}

// ---------- isNotFound ----------

func TestIsNotFound_True(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/missing.txt", func(w http.ResponseWriter, r *http.Request) {
		respondNotFound(w)
	})
	_, err := fsys.Stat("missing.txt")
	if err == nil {
		t.Fatal("expected error")
	}
	// Verify it wraps ErrNotExist
	perr, ok := err.(*os.PathError)
	if !ok {
		t.Fatalf("expected PathError, got %T", err)
	}
	if perr.Err != ErrNotExist {
		t.Errorf("expected ErrNotExist, got %v", perr.Err)
	}
}

// ---------- getSHA / setSHA ----------

func TestGetSetSHA(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	fsys.setSHA("foo/bar.txt", "abc123")
	got := fsys.getSHA("foo/bar.txt")
	if got != "abc123" {
		t.Errorf("getSHA = %q, want abc123", got)
	}
}

func TestGetSetSHA_LeadingSlash(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	fsys.setSHA("/foo/bar.txt", "sha1")
	got := fsys.getSHA("foo/bar.txt")
	if got != "sha1" {
		t.Errorf("getSHA = %q after slash-normalised set", got)
	}
}

// ---------- cacheValid ----------

func TestCacheValid_Miss(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	if fsys.cacheValid("anything") {
		t.Error("expected cache miss for uncached path")
	}
}

func TestCacheValid_Hit(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	fsys.warmCache("foo.txt", []byte("content"))
	if !fsys.cacheValid("foo.txt") {
		t.Error("expected cache hit")
	}
}

func TestCacheValid_Stale(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b", shortTTLOpt())
	fsys.warmCache("foo.txt", []byte("content"))
	time.Sleep(5 * time.Millisecond)
	if fsys.cacheValid("foo.txt") {
		t.Error("expected cache to be stale")
	}
}

// ---------- evict ----------

func TestEvict(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	fsys.warmCache("foo.txt", []byte("hello"))
	fsys.setSHA("foo.txt", "sha1")

	fsys.evict("foo.txt")

	if fsys.cacheValid("foo.txt") {
		t.Error("expected cache to be evicted")
	}
	if fsys.getSHA("foo.txt") != "" {
		t.Error("expected SHA to be evicted")
	}
}

// ---------- warmCache ----------

func TestWarmCache(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	fsys.warmCache("sub/file.txt", []byte("data"))

	if !fsys.cacheValid("sub/file.txt") {
		t.Error("expected cache to be valid after warmCache")
	}
	// Verify content is accessible from memFs
	f, err := fsys.memFs.Open("sub/file.txt")
	if err != nil {
		t.Fatalf("memFs.Open error: %v", err)
	}
	defer f.Close()
}

// ---------- writeMemFile ----------

func TestWriteMemFile(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	if err := writeMemFile(fsys.memFs, "test.txt", []byte("content")); err != nil {
		t.Fatalf("writeMemFile error: %v", err)
	}
}

// ---------- githubItemToFileInfo ----------

func TestGithubItemToFileInfo_File(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/subdir", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(dirContentJSON([]dirEntrySpec{
			{Path: "subdir/file.txt", SHA: "sha1", Size: 10},
		})))
	})
	_, entries, err := fsys.readDirIfDir("subdir")
	if err != nil {
		t.Fatalf("readDirIfDir error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	fi := entries[0]
	if fi.IsDir() {
		t.Error("expected file, got dir")
	}
	if fi.Size() != 10 {
		t.Errorf("Size() = %d, want 10", fi.Size())
	}
}

func TestGithubItemToFileInfo_Dir(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/root", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(dirContentJSON([]dirEntrySpec{
			{Path: "root/subdir", IsDir: true},
		})))
	})
	_, entries, _ := fsys.readDirIfDir("root")
	if len(entries) == 0 {
		t.Fatal("expected entry")
	}
	if !entries[0].IsDir() {
		t.Error("expected dir entry")
	}
}

// ---------- fetchFile ----------

func TestFetchFile_Success(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/file.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fileContentJSON("file.txt", "hello world", "sha1", 11)))
	})
	content, err := fsys.fetchFile("file.txt")
	if err != nil {
		t.Fatalf("fetchFile error: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("fetchFile content = %q", string(content))
	}
}

func TestFetchFile_NotFound(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/missing.txt", func(w http.ResponseWriter, r *http.Request) {
		respondNotFound(w)
	})
	_, err := fsys.fetchFile("missing.txt")
	if err != ErrNotExist {
		t.Errorf("expected ErrNotExist, got %v", err)
	}
}

func TestFetchFile_APIError(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/err.txt", func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusInternalServerError, "server error")
	})
	_, err := fsys.fetchFile("err.txt")
	if err == nil {
		t.Error("expected error")
	}
}

func TestFetchFile_DirResponse(t *testing.T) {
	// When GitHub returns a dir array instead of file content, fetchFile returns ErrNotExist.
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/adir", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(dirContentJSON([]dirEntrySpec{{Path: "adir/a.txt", SHA: "s1"}})))
	})
	_, err := fsys.fetchFile("adir")
	if err != ErrNotExist {
		t.Errorf("expected ErrNotExist for dir response, got %v", err)
	}
}

// ---------- getContent ----------

func TestGetContent_FromCache(t *testing.T) {
	fsys, _, _ := newTestFs(t, "owner", "repo", "main")
	fsys.warmCache("file.txt", []byte("cached"))
	content, err := fsys.getContent("file.txt")
	if err != nil {
		t.Fatalf("getContent error: %v", err)
	}
	if string(content) != "cached" {
		t.Errorf("getContent = %q, want cached", string(content))
	}
}

func TestGetContent_FromAPI(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/file.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fileContentJSON("file.txt", "from api", "sha1", 8)))
	})
	content, err := fsys.getContent("file.txt")
	if err != nil {
		t.Fatalf("getContent error: %v", err)
	}
	if string(content) != "from api" {
		t.Errorf("getContent = %q", string(content))
	}
}

// ---------- ensureSHA ----------

func TestEnsureSHA_Cached(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	fsys.setSHA("file.txt", "abc")
	sha, err := fsys.ensureSHA("file.txt")
	if err != nil || sha != "abc" {
		t.Errorf("ensureSHA = %q, %v", sha, err)
	}
}

func TestEnsureSHA_FromStat(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/file.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fileContentJSON("file.txt", "data", "mysha", 4)))
	})
	sha, err := fsys.ensureSHA("file.txt")
	if err != nil {
		t.Fatalf("ensureSHA error: %v", err)
	}
	if sha != "mysha" {
		t.Errorf("ensureSHA = %q, want mysha", sha)
	}
}

func TestEnsureSHA_NotFound(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/missing.txt", func(w http.ResponseWriter, r *http.Request) {
		respondNotFound(w)
	})
	_, err := fsys.ensureSHA("missing.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestEnsureSHA_NoSHAAfterStat(t *testing.T) {
	// Stat succeeds (returns dir) but doesn't populate SHA - so ensureSHA returns error
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/adir", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(dirContentJSON([]dirEntrySpec{{Path: "adir/x.txt", SHA: "s1"}})))
	})
	_, err := fsys.ensureSHA("adir")
	if err == nil {
		t.Error("expected error when SHA not populated")
	}
}

// ---------- readDir ----------

func TestReadDir_Success(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/mydir", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(dirContentJSON([]dirEntrySpec{
			{Path: "mydir/a.txt", SHA: "sha1", Size: 5},
			{Path: "mydir/b.txt", SHA: "sha2", Size: 3},
		})))
	})
	entries, err := fsys.readDir("mydir")
	if err != nil {
		t.Fatalf("readDir error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("readDir len = %d, want 2", len(entries))
	}
}

func TestReadDir_NotFound(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/nodir", func(w http.ResponseWriter, r *http.Request) {
		respondNotFound(w)
	})
	_, err := fsys.readDir("nodir")
	if err == nil {
		t.Error("expected error for missing dir")
	}
}

func TestReadDir_APIError(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/errdir", func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusInternalServerError, "boom")
	})
	_, err := fsys.readDir("errdir")
	if err == nil {
		t.Error("expected error")
	}
}

// ---------- readDirIfDir ----------

func TestReadDirIfDir_IsDir(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/d", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(dirContentJSON([]dirEntrySpec{{Path: "d/f.txt", SHA: "s1"}})))
	})
	isDir, entries, err := fsys.readDirIfDir("d")
	if err != nil || !isDir {
		t.Errorf("readDirIfDir: isDir=%v err=%v", isDir, err)
	}
	if len(entries) != 1 {
		t.Errorf("readDirIfDir entries=%d", len(entries))
	}
}

func TestReadDirIfDir_IsFile(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/file.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fileContentJSON("file.txt", "content", "sha1", 7)))
	})
	isDir, _, err := fsys.readDirIfDir("file.txt")
	if err != nil || isDir {
		t.Errorf("readDirIfDir: isDir=%v err=%v, want isDir=false", isDir, err)
	}
}

func TestReadDirIfDir_Error(t *testing.T) {
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/x", func(w http.ResponseWriter, r *http.Request) {
		respondNotFound(w)
	})
	// 404 is treated as "not a dir", no error
	isDir, _, err := fsys.readDirIfDir("x")
	if err != nil || isDir {
		t.Errorf("readDirIfDir: isDir=%v err=%v", isDir, err)
	}
}

// ---------- wrapCached ----------

func TestWrapCached_RDONLY(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	f := wrapCached(fsys, "file.txt", []byte("hi"), os.O_RDONLY)
	if f.reader == nil {
		t.Error("expected reader for O_RDONLY")
	}
}

func TestWrapCached_RDWR(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	f := wrapCached(fsys, "file.txt", []byte("hi"), os.O_RDWR)
	if f.reader == nil {
		t.Error("expected reader for O_RDWR")
	}
}

func TestWrapCached_WRONLY(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	f := wrapCached(fsys, "file.txt", []byte("hi"), os.O_WRONLY)
	if f.reader != nil {
		t.Error("expected no reader for O_WRONLY")
	}
}

// ---------- httpClient ----------

func TestHttpClient(t *testing.T) {
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	hc := fsys.httpClient()
	if hc == nil {
		t.Error("expected non-nil http.Client")
	}
}

// ---------- isNotFound with non-GitHub error ----------

func TestIsNotFound_NonGitHubError(t *testing.T) {
	// When the error is not a *github.ErrorResponse, isNotFound returns false.
	// This exercises the "return false" branch.
	if isNotFound(os.ErrNotExist) {
		t.Error("isNotFound should return false for non-GitHub error")
	}
}

// ---------- fetchFile GetContent error (line 48-50) ----------

func TestFetchFile_GetContentError(t *testing.T) {
	// Exercises fs_helpers.go:48-50: fileContent.GetContent() returns an error.
	// GetContent() fails when encoding is "base64" but content is not valid base64.
	// Using raw invalid base64 in the content field with base64 encoding triggers
	// the GetContent error path before our manual decode.
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/getcontent_err.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Invalid base64 content: GetContent will fail to decode it.
		w.Write([]byte(`{
			"type": "file",
			"name": "getcontent_err.txt",
			"path": "getcontent_err.txt",
			"sha": "abc",
			"size": 5,
			"content": "!!!notvalidbase64!!!",
			"encoding": "base64"
		}`))
	})
	_, err := fsys.fetchFile("getcontent_err.txt")
	if err == nil {
		t.Error("expected error when GetContent fails")
	}
}

// ---------- fetchFile base64 decode error ----------

func TestFetchFile_Base64DecodeError(t *testing.T) {
	// Exercises the second base64 decode error in fetchFile (line 52-57).
	// fetchFile calls GetContent() which decodes the outer base64 layer, then
	// manually calls base64.StdEncoding.DecodeString on the result.
	// To hit the second-decode error path: the JSON content field must be
	// base64-encoded (so GetContent succeeds) but the decoded value must
	// itself be invalid base64.
	//
	// We use b64("!!!not-base64!!!") as the content field: GetContent decodes
	// the outer b64 → "!!!not-base64!!!", then the manual decode fails.
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	invalidInnerContent := b64("!!!not-base64!!!")
	mux.HandleFunc("/repos/owner/repo/contents/badb64.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"type": "file",
			"name": "badb64.txt",
			"path": "badb64.txt",
			"sha": "abc",
			"size": 5,
			"content": "` + invalidInnerContent + `",
			"encoding": "base64"
		}`))
	})
	_, err := fsys.fetchFile("badb64.txt")
	if err == nil {
		t.Error("expected error for second-pass base64 decode failure")
	}
}

// ---------- newFileForWrite: getContent error ----------

func TestNewFileForWrite_GetContentError(t *testing.T) {
	// Exercises the getContent error branch in newFileForWrite when O_RDWR|O_TRUNC
	// is used (which calls newFileForWrite via truncate path) and getContent returns
	// a non-ErrNotExist error.
	fsys, mux, _ := newTestFs(t, "owner", "repo", "main")
	mux.HandleFunc("/repos/owner/repo/contents/rdwrerr.txt", func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusInternalServerError, "boom")
	})
	// O_RDWR|O_TRUNC: truncate=true → newFileForWrite is called.
	// isRdWr=true → getContent is called → API 500 → non-ErrNotExist → error returned.
	_, err := fsys.OpenFile("rdwrerr.txt", os.O_RDWR|os.O_TRUNC, 0644)
	if err == nil {
		t.Error("expected error from OpenFile(O_RDWR|O_TRUNC) when getContent fails")
	}
}

// ---------- getReader: reader=nil sets from buf ----------

func TestGetReader_FromBuf(t *testing.T) {
	// getReader when reader is nil: this is reached via Seek in write-only mode
	// where !readable() is true - but Seek's write-only branch doesn't call
	// getReader. getReader is called only from Read/ReadAt/Seek when readable().
	// For O_RDWR with no existing content: newFileForWrite sets reader only
	// when len(existing) > 0. If no content found, reader stays nil.
	// Then Read → readable() = true → getReader() → builds from buf.
	//
	// Build directly: O_RDWR file, reader explicitly nil.
	fsys, _, _ := newTestFs(t, "o", "r", "b")
	f := &File{
		fs:   fsys,
		path: "new.txt",
		name: "new.txt",
		buf:  newBufferFrom([]byte("hello")),
		flag: os.O_RDWR,
		// reader is intentionally nil
	}
	// getReader will build reader from buf.
	r := f.getReader()
	if r == nil {
		t.Error("expected non-nil reader from getReader")
	}
	if r.Size() != 5 {
		t.Errorf("reader size = %d, want 5", r.Size())
	}
	// Call getReader again to cover the non-nil branch.
	r2 := f.getReader()
	if r2 != r {
		t.Error("expected same reader on second call")
	}
}

// ---------- writeMemFile error ----------

func TestWriteMemFile_Error(t *testing.T) {
	// writeMemFile returns an error when OpenFile on the underlying MemFs fails.
	// Use a read-only MemMapFs wrapper to force an error.
	roFs := afero.NewReadOnlyFs(afero.NewMemMapFs())
	err := writeMemFile(roFs, "test.txt", []byte("data"))
	if err == nil {
		t.Error("expected error writing to read-only fs")
	}
}
