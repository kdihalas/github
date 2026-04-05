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

func (fs *Fs) httpClient() *http.Client {
	return fs.github().Client()
}

// fetchFile calls the GitHub Contents API and decodes the file content.
// It also populates the SHA cache.
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

// getContent returns content from MemMapFs if fresh, otherwise fetches from GitHub.
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

// ensureSHA returns the SHA for path, fetching from GitHub if not cached.
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

// readDir lists the children of a directory via the GitHub Contents API.
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

// readDirIfDir checks if path is a directory and returns its entries.
// Returns (false, nil, nil) if path is a file.
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

// isDir checks if the given path is a directory by querying GitHub. It returns true if it's a directory, false if it's a file, and an error if the path does not exist or another error occurs.
func (fs *Fs) warmCache(path string, content []byte) {
	_ = fs.memFs.MkdirAll(filepath.Dir(path), 0755)
	_ = writeMemFile(fs.memFs, path, content)

	fs.ttlMu.Lock()
	fs.ttlCache[path] = time.Now()
	fs.ttlMu.Unlock()
}

// cacheValid checks if the cache entry for the given path is still valid based on the TTL. It returns true if the cache is valid, false otherwise.
func (fs *Fs) cacheValid(path string) bool {
	fs.ttlMu.RLock()
	t, ok := fs.ttlCache[path]
	fs.ttlMu.RUnlock()
	return ok && time.Since(t) < fs.cacheTTL
}

// evict removes the cache entry for the given path from both the MemMapFs and the SHA/TTL caches.
func (fs *Fs) evict(path string) {
	_ = fs.memFs.Remove(path)

	fs.ttlMu.Lock()
	delete(fs.ttlCache, path)
	fs.ttlMu.Unlock()

	fs.shaMu.Lock()
	delete(fs.shaCache, path)
	fs.shaMu.Unlock()
}

// getSHA retrieves the cached SHA for the given path. It returns an empty string if no SHA is cached.
func (fs *Fs) getSHA(path string) string {
	fs.shaMu.RLock()
	defer fs.shaMu.RUnlock()
	return fs.shaCache[cleanPath(path)]
}

// setSHA updates the cached SHA for the given path.
func (fs *Fs) setSHA(path, sha string) {
	fs.shaMu.Lock()
	fs.shaCache[cleanPath(path)] = sha
	fs.shaMu.Unlock()
}

// newContext creates a new context with the configured API timeout for GitHub requests.
func (fs *Fs) newContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), fs.apiTimeout)
}

// wrapCached builds a GitHubFile from already-fetched content.
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

// writeMemFile writes bytes to a MemMapFs file, creating it if needed.
func writeMemFile(mfs afero.Fs, path string, content []byte) error {
	f, err := mfs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	_ = f.Close()
	return err
}

// githubItemToFileInfo converts a GitHub RepositoryContent item to an os.FileInfo implementation, extracting the name, size, and type (file or directory) information.
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

// isNotFound checks if the error is a GitHub 404 Not Found error.
func isNotFound(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) {
		return ghErr.Response != nil && ghErr.Response.StatusCode == 404
	}
	return false
}

// cleanPath normalises a path: trims leading slashes, cleans double slashes.
func cleanPath(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "/")
}

// newBufferFrom creates a bytes.Buffer pre-filled with content.
func newBufferFrom(content []byte) *bytes.Buffer {
	buf := bytes.NewBuffer(make([]byte, 0, len(content)))
	buf.Write(content)
	return buf
}

// newReaderFrom creates a bytes.Reader over a copy of content.
// A copy is taken so that later writes to the buffer don't affect the reader's
// underlying slice.
func newReaderFrom(content []byte) *bytes.Reader {
	cp := make([]byte, len(content))
	copy(cp, content)
	return bytes.NewReader(cp)
}
