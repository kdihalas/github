package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	gh "github.com/google/go-github/v84/github"
	"github.com/spf13/afero"
)

// httpClient returns the underlying HTTP client from the GitHub API client.
func (fs *Fs) httpClient() *http.Client {
	return fs.github().Client()
}

// fetchFile retrieves file content from the GitHub Contents API, decodes the base64-encoded response,
// and populates the SHA cache. Returns ErrNotExist if the path does not exist.
func (fs *Fs) fetchFile(path string) ([]byte, error) {
	ctx, cancel := fs.newContext()
	defer cancel()

	fileContent, _, _, err := fs.github().Repositories.GetContents(
		ctx, *fs.owner, *fs.repo, path,
		&gh.RepositoryContentGetOptions{Ref: *fs.branch},
	)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotExist
		}
		return nil, err
	}
	if fileContent == nil {
		return nil, ErrNotExist
	}

	if fileContent.SHA != nil {
		fs.setSHA(path, *fileContent.SHA)
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(content, "\n", ""),
	)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// getContent returns file content from the in-memory cache if it is valid,
// otherwise fetches from GitHub and populates the cache.
func (fs *Fs) getContent(path string) ([]byte, error) {
	if fs.cacheValid(path) {
		if f, err := fs.memFs.Open(path); err == nil {
			defer func() { _ = f.Close() }()
			return afero.ReadAll(f)
		}
	}
	content, err := fs.fetchFile(path)
	if err != nil {
		return nil, err
	}
	fs.warmCache(path, content)
	return content, nil
}

// ensureSHA retrieves the blob SHA for the path from the cache if present,
// otherwise calls Stat to populate it. Returns an error if the path does not exist.
// The SHA is required by the GitHub API for update and delete operations.
func (fs *Fs) ensureSHA(path string) (string, error) {
	if sha := fs.getSHA(path); sha != "" {
		return sha, nil
	}
	// Stat will populate the SHA cache as a side-effect.
	if _, err := fs.Stat(path); err != nil {
		return "", err
	}
	sha := fs.getSHA(path)
	if sha == "" {
		return "", errors.New("could not determine SHA for " + path)
	}
	return sha, nil
}

// readDir lists the directory contents via the GitHub Contents API,
// caching the blob SHA for each child entry.
// Returns an os.PathError with ErrNotExist if the directory does not exist.
func (fs *Fs) readDir(path string) ([]os.FileInfo, error) {
	ctx, cancel := fs.newContext()
	defer cancel()

	_, dirContent, _, err := fs.github().Repositories.GetContents(
		ctx, *fs.owner, *fs.repo, path,
		&gh.RepositoryContentGetOptions{Ref: *fs.branch},
	)
	if err != nil {
		if isNotFound(err) {
			return nil, &os.PathError{Op: "readdir", Path: path, Err: ErrNotExist}
		}
		return nil, err
	}

	entries := make([]os.FileInfo, 0, len(dirContent))
	for _, item := range dirContent {
		if item.SHA != nil && item.Path != nil {
			fs.setSHA(*item.Path, *item.SHA)
		}
		entries = append(entries, githubItemToFileInfo(item))
	}
	return entries, nil
}

// readDirIfDir checks whether the path is a directory by querying the GitHub Contents API.
// If it is a directory, it returns (true, entries, nil).
// If it is a file or does not exist, it returns (false, nil, nil).
// It caches the blob SHA for each child entry as a side-effect.
func (fs *Fs) readDirIfDir(path string) (bool, []os.FileInfo, error) {
	ctx, cancel := fs.newContext()
	defer cancel()

	_, dirContent, _, err := fs.github().Repositories.GetContents(
		ctx, *fs.owner, *fs.repo, path,
		&gh.RepositoryContentGetOptions{Ref: *fs.branch},
	)
	if err != nil {
		return false, nil, nil // treat error as "not a dir", let callers handle
	}
	if dirContent == nil {
		return false, nil, nil
	}

	entries := make([]os.FileInfo, 0, len(dirContent))
	for _, item := range dirContent {
		if item.SHA != nil && item.Path != nil {
			fs.setSHA(*item.Path, *item.SHA)
		}
		entries = append(entries, githubItemToFileInfo(item))
	}
	return true, entries, nil
}

// warmCache populates the in-memory cache, SHA cache, and TTL cache with the given content.
// It creates any necessary parent directories in the MemMapFs layer.
func (fs *Fs) warmCache(path string, content []byte) {
	_ = fs.memFs.MkdirAll(filepath.Dir(path), 0755)
	_ = writeMemFile(fs.memFs, path, content)

	fs.ttlMu.Lock()
	fs.ttlCache[path] = time.Now()
	fs.ttlMu.Unlock()
}

// cacheValid checks whether the cache entry for the given path has not expired based on the TTL.
// It returns true if the entry exists and is still fresh, false otherwise.
func (fs *Fs) cacheValid(path string) bool {
	fs.ttlMu.RLock()
	t, ok := fs.ttlCache[path]
	fs.ttlMu.RUnlock()
	return ok && time.Since(t) < fs.cacheTTL
}

// evict removes the cache entry for the path from the MemMapFs, SHA cache, and TTL cache.
func (fs *Fs) evict(path string) {
	_ = fs.memFs.Remove(path)

	fs.ttlMu.Lock()
	delete(fs.ttlCache, path)
	fs.ttlMu.Unlock()

	fs.shaMu.Lock()
	delete(fs.shaCache, path)
	fs.shaMu.Unlock()
}

// getSHA retrieves the cached blob SHA for the path, returning an empty string if not cached.
// The path is cleaned before lookup.
func (fs *Fs) getSHA(path string) string {
	fs.shaMu.RLock()
	defer fs.shaMu.RUnlock()
	return fs.shaCache[cleanPath(path)]
}

// setSHA stores the blob SHA for the path in the cache.
// The path is cleaned before storage.
func (fs *Fs) setSHA(path, sha string) {
	fs.shaMu.Lock()
	fs.shaCache[cleanPath(path)] = sha
	fs.shaMu.Unlock()
}

// newContext creates a new context with the configured API timeout.
// Each API request is canceled if it exceeds this duration.
func (fs *Fs) newContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), fs.apiTimeout)
}

// wrapCached creates a File from already-fetched content.
// If the file is opened for reading (O_RDONLY or O_RDWR), it sets up a reader.
// The write buffer is always prepared with the content.
func wrapCached(fs *Fs, path string, content []byte, flag int) *File {
	f := &File{
		fs:   fs,
		path: path,
		name: filepath.Base(path),
		buf:  newBufferFrom(content),
		flag: flag,
	}
	if flag == os.O_RDONLY || flag&os.O_RDWR != 0 {
		f.reader = newReaderFrom(content)
	}
	return f
}

// writeMemFile writes the given content to a file in the MemMapFs, creating or truncating as necessary.
func writeMemFile(mfs afero.Fs, path string, content []byte) error {
	f, err := mfs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	_ = f.Close()
	return err
}

// githubItemToFileInfo converts a GitHub RepositoryContent item to a GitHubFileInfo,
// extracting the base name, size, and file/directory type.
func githubItemToFileInfo(item *gh.RepositoryContent) os.FileInfo {
	isDir := item.GetType() == "dir"
	size := int64(0)
	if item.Size != nil {
		size = int64(*item.Size)
	}
	return &GitHubFileInfo{
		name:  filepath.Base(item.GetPath()),
		size:  size,
		isDir: isDir,
	}
}

// isNotFound checks whether the error is a GitHub 404 Not Found response.
// It unwraps a github.ErrorResponse and checks the HTTP status code.
func isNotFound(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) {
		return ghErr.Response != nil && ghErr.Response.StatusCode == 404
	}
	return false
}

// cleanPath normalizes a path by converting backslashes to forward slashes,
// cleaning up double slashes and . segments, and trimming any leading slash.
// This is the canonical path format used for all cache keys.
func cleanPath(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "/")
}

// newBufferFrom creates a bytes.Buffer with preallocated capacity and populated with content.
func newBufferFrom(content []byte) *bytes.Buffer {
	buf := bytes.NewBuffer(make([]byte, 0, len(content)))
	buf.Write(content)
	return buf
}

// newReaderFrom creates a bytes.Reader over a copy of the content.
// A copy is made so that later writes to the buffer do not affect the reader's underlying slice.
func newReaderFrom(content []byte) *bytes.Reader {
	cp := make([]byte, len(content))
	copy(cp, content)
	return bytes.NewReader(cp)
}
