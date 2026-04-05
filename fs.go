package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v84/github"
	"github.com/spf13/afero"
)

const (
	defaultCacheTTL   = 30 * time.Second
	defaultAPITimeout = 15 * time.Second
	gitKeepFile       = ".gitkeep"
)

// ErrNotImplemented is returned when an operation is not (yet) implemented.
var ErrNotImplemented = errors.New("not implemented")

// ErrNotSupported is returned when an operation is not supported by the GitHub filesystem.
// This includes Chmod, Chown, and Chtimes, which require metadata that GitHub does not support.
var ErrNotSupported = errors.New("git fs doesn't support this operation")

// ErrAlreadyOpened is returned when attempting to open a file that is already open.
var ErrAlreadyOpened = errors.New("already opened")

// ErrInvalidSeek is returned when a Seek operation is invalid (e.g., negative position).
var ErrInvalidSeek = errors.New("invalid seek offset")

// ErrExist is returned when Create or Mkdir is called for a path that already exists.
var ErrExist = errors.New("already exists")

// ErrNotExist is returned when a file or directory does not exist.
var ErrNotExist = errors.New("does not exist")

// Fs is an afero.Fs implementation backed by a GitHub repository.
// It translates filesystem operations into GitHub Contents API calls.
// Path operations are cached with a configurable TTL to minimize API calls.
type Fs struct {
	client              *Client
	owner, repo, branch *string // Repository owner, name, and branch (if specified)
	// MemMapFs acts as the content cache layer.
	memFs afero.Fs

	// SHA cache: GitHub requires the blob SHA for updates and deletes.
	shaMu    sync.RWMutex
	shaCache map[string]string // normalised path → blob SHA

	// TTL cache metadata: tracks when a path was last fetched from the API.
	ttlMu    sync.RWMutex
	ttlCache map[string]time.Time // normalised path → last fetch time
	cacheTTL time.Duration

	// apiTimeout is the per-request context timeout.
	apiTimeout time.Duration

	// commitAuthor is optional; used in commit messages.
	commitAuthor *gh.CommitAuthor
}

// Option is a functional option for configuring an Fs instance.
type Option func(*Fs)

// WithCacheTTL sets the cache time-to-live for filesystem entries.
// Default is 30 seconds. Cached entries are considered fresh until this duration has elapsed.
func WithCacheTTL(d time.Duration) Option { return func(g *Fs) { g.cacheTTL = d } }

// WithAPITimeout sets the per-request timeout for GitHub API calls.
// Default is 15 seconds. Each Stat, Open, Read, or Write operation has this timeout.
func WithAPITimeout(d time.Duration) Option { return func(g *Fs) { g.apiTimeout = d } }

// WithCommitAuthor sets the author name and email stamped on every commit made via this filesystem.
// If not set, GitHub uses the authenticated user's information.
func WithCommitAuthor(name, email string) Option {
	return func(g *Fs) {
		g.commitAuthor = &gh.CommitAuthor{Name: &name, Email: &email}
	}
}

// NewFsFromClient creates a new Fs instance using the provided Client and repository reference.
// The owner, repo, and branch strings are normalized (leading slashes trimmed, paths cleaned).
// Caching is enabled with default TTL and API timeout unless overridden via options.
func NewFsFromClient(client *Client, owner, repo, branch string, opts ...Option) *Fs {
	fs := &Fs{
		client:     client,
		owner:      &owner,
		repo:       &repo,
		branch:     &branch,
		memFs:      afero.NewMemMapFs(),
		shaCache:   make(map[string]string),
		ttlCache:   make(map[string]time.Time),
		cacheTTL:   defaultCacheTTL,
		apiTimeout: defaultAPITimeout,
	}
	for _, o := range opts {
		o(fs)
	}
	return fs
}

// Name returns the name of the filesystem implementation, which is "github".
func (*Fs) Name() string { return "github" }

// Owner returns the repository owner. It dereferences the internal pointer and returns an empty string if not set.
func (fs *Fs) Owner() string {
	if fs.owner == nil {
		return ""
	}
	return *fs.owner
}

// Repo returns the repository name. It dereferences the internal pointer and returns an empty string if not set.
func (fs *Fs) Repo() string {
	if fs.repo == nil {
		return ""
	}
	return *fs.repo
}

// Branch returns the branch name. It dereferences the internal pointer and returns an empty string if not set.
func (fs *Fs) Branch() string {
	if fs.branch == nil {
		return ""
	}
	return *fs.branch
}

// Create creates or truncates the named file for reading and writing.
// It is equivalent to OpenFile with flags O_RDWR|O_CREATE|O_TRUNC and mode 0644.
func (fs *Fs) Create(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
}

// Mkdir creates a directory with the given name. Since GitHub has no native directory support,
// this is implemented by creating a placeholder .gitkeep file inside the directory.
// Returns ErrExist if the directory already exists.
func (fs *Fs) Mkdir(name string, perm os.FileMode) error {
	name = cleanPath(name)

	// Already exists?
	if _, err := fs.Stat(name); err == nil {
		return &os.PathError{Op: "mkdir", Path: name, Err: ErrExist}
	}

	// GitHub doesn't have real directories, so we create a placeholder file to represent the directory.
	keepPath := name + "/" + gitKeepFile
	f, err := fs.OpenFile(keepPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: name, Err: err}
	}
	return f.Close()
}

// MkdirAll creates the directory and all necessary parent directories.
// It checks each path component and creates missing directories as needed.
// Returns nil if the directory already exists.
func (fs *Fs) MkdirAll(path string, perm os.FileMode) error {
	path = cleanPath(path)
	parts := strings.Split(path, "/")

	// Iteratively check each level of the path and create directories as needed.
	var cumulative string
	for _, part := range parts {
		if part == "" {
			continue
		}
		if cumulative == "" {
			cumulative = part
		} else {
			cumulative = cumulative + "/" + part
		}
		if _, err := fs.Stat(cumulative); errors.Is(err, ErrNotExist) {
			if err2 := fs.Mkdir(cumulative, perm); err2 != nil {
				return err2
			}
		}
	}
	return nil
}

// Open opens the named file for reading. It is equivalent to OpenFile with O_RDONLY.
// The file's content is fetched from GitHub if not cached, or served from the in-memory cache if valid.
func (fs *Fs) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

// OpenFile opens a file with the given flags and permissions.
// It implements the afero.Fs interface and handles files and directories.
//
// For directories, it returns a File with preloaded directory entries.
// For regular files, it tries the cache first (if valid), then fetches from GitHub.
// Write operations (O_WRONLY, O_TRUNC) pre-load existing content if O_APPEND or O_RDWR is set,
// ensuring the buffer reflects the correct base content for flush operations.
// If O_CREATE is set and the file doesn't exist, it prepares a new file for writing.
func (fs *Fs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	name = cleanPath(name)

	// Check if it's a directory first, since GitHub returns file content for directories as well.
	if isDir, entries, err := fs.readDirIfDir(name); isDir {
		if err != nil {
			return nil, err
		}
		return newDir(fs, name, entries), nil
	}

	// Handle write modes: O_WRONLY (without O_RDWR) or O_TRUNC always go through the write path, which prepares the buffer and reader correctly for flush.
	writeOnly := flag&os.O_WRONLY != 0 && flag&os.O_RDWR == 0
	truncate := flag&os.O_TRUNC != 0
	create := flag&os.O_CREATE != 0

	// If it's a write operation, we need to prepare the file for writing, which may involve pre-loading existing content
	// if O_APPEND or O_RDWR is set. This ensures that the buffer has the correct base content for any subsequent writes and flushes.
	if writeOnly || truncate {
		return newFileForWrite(fs, name, flag)
	}

	// For read-only operations, we can attempt to serve from cache first, which is faster. If the cache is valid and the file exists in the cache,
	// we wrap it in a File struct that still allows for flushing to GitHub on writes.
	if fs.cacheValid(name) {
		cf, err := fs.memFs.OpenFile(name, flag, perm)
		if err == nil {
			// Wrap in a File so writes still flush to GitHub.
			content, _ := afero.ReadAll(cf)
			_ = cf.Close()
			return wrapCached(fs, name, content, flag), nil
		}
	}

	// Not in cache or not valid, fetch from GitHub. If the file doesn't exist and O_CREATE is set, we prepare a new file for writing.
	content, err := fs.fetchFile(name)
	if err != nil {
		if errors.Is(err, ErrNotExist) && create {
			return newFileForWrite(fs, name, flag)
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}

	// Warm the cache with the fetched content and return a wrapped File that allows for flushing on writes.
	fs.warmCache(name, content)
	return wrapCached(fs, name, content, flag), nil
}

// Remove deletes the named file from the repository.
// The file must exist; if it does not, an error is returned.
// The operation uses the GitHub Contents API DeleteFile method,
// which requires the current blob SHA (populated from the cache or fetched on demand).
// After successful deletion, the file is evicted from all internal caches.
func (fs *Fs) Remove(name string) error {
	name = cleanPath(name)

	// Ensure the file exists and get its SHA, which is required for deletion.
	sha, err := fs.ensureSHA(name)
	if err != nil {
		return &os.PathError{Op: "remove", Path: name, Err: err}
	}

	// Construct the commit message and options for the GitHub API call to delete the file. The commit message is prefixed with "chore: remove" followed by the file name.
	message := "chore: remove " + name
	opts := &gh.RepositoryContentFileOptions{
		Message: &message,
		SHA:     &sha,
		Branch:  fs.branch,
	}
	if fs.commitAuthor != nil {
		opts.Author = fs.commitAuthor
	}

	ctx, cancel := fs.newContext()
	defer cancel()

	// Call the GitHub API to delete the file. If an error occurs, it returns an os.PathError with the appropriate operation, path, and error details.
	_, _, err = fs.github().Repositories.DeleteFile(ctx, fs.Owner(), fs.Repo(), name, opts)
	if err != nil {
		return &os.PathError{Op: "remove", Path: name, Err: err}
	}

	fs.evict(name)
	return nil
}

// RemoveAll deletes the path and all its contents recursively.
// If the path is a directory, it recursively deletes all child files and directories.
// If the path does not exist, RemoveAll returns nil (no error).
func (fs *Fs) RemoveAll(path string) error {
	path = cleanPath(path)

	info, err := fs.Stat(path)
	if errors.Is(err, ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return fs.Remove(path)
	}

	// List and delete all children first.
	entries, err := fs.readDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := path + "/" + entry.Name()
		if err := fs.RemoveAll(child); err != nil {
			return err
		}
	}
	return nil
}

// Rename moves the file at oldname to newname.
// It reads the source file's content, creates it at the destination, and deletes the source.
// Returns an error if any step fails.
func (fs *Fs) Rename(oldname, newname string) error {
	oldname = cleanPath(oldname)
	newname = cleanPath(newname)

	// Read source.
	content, err := fs.getContent(oldname)
	if err != nil {
		return &os.PathError{Op: "rename", Path: oldname, Err: err}
	}

	// Write to destination.
	dst, err := fs.Create(newname)
	if err != nil {
		return &os.PathError{Op: "rename", Path: newname, Err: err}
	}
	if _, err = dst.Write(content); err != nil {
		_ = dst.Close()
		return err
	}
	if err = dst.Close(); err != nil {
		return err
	}

	// Delete source.
	return fs.Remove(oldname)
}

// Stat retrieves the FileInfo for the given path.
// It checks the cache first (if valid) and queries GitHub if not.
// Returns an os.PathError with ErrNotExist if the path does not exist.
// The operation populates the SHA cache as a side-effect, which is necessary for future writes and deletes.
func (fs *Fs) Stat(name string) (os.FileInfo, error) {
	name = cleanPath(name)

	// Check cache first for a valid entry, which is faster. If the cache is valid and the file exists in the cache, it returns the cached FileInfo.
	// If the cache is not valid or the file is not in the cache, it proceeds to query GitHub.
	if fs.cacheValid(name) {
		if fi, err := fs.memFs.Stat(name); err == nil {
			return fi, nil
		}
	}

	ctx, cancel := fs.newContext()
	defer cancel()

	// GitHub's Contents API returns file content for both files and directories, so we need to check both responses to determine if it's a file or directory.
	fileContent, dirContent, _, err := fs.github().Repositories.GetContents(
		ctx, fs.Owner(), fs.Repo(), name,
		&gh.RepositoryContentGetOptions{Ref: fs.Branch()},
	)
	if err != nil {
		if isNotFound(err) {
			return nil, &os.PathError{Op: "stat", Path: name, Err: ErrNotExist}
		}
		return nil, &os.PathError{Op: "stat", Path: name, Err: err}
	}

	// Directory response: if dirContent is not nil, it means the path is a directory. We convert the directory entries to FileInfo and return a GitHubFileInfo representing the directory.
	if dirContent != nil {
		return &GitHubFileInfo{
			name:  filepath.Base(name),
			isDir: true,
		}, nil
	}

	// File response: if fileContent is not nil, it means the path is a file. We populate the SHA cache and return a GitHubFileInfo representing the file.
	if fileContent != nil {
		if fileContent.SHA != nil {
			fs.setSHA(name, *fileContent.SHA)
		}
		size := int64(0)
		if fileContent.Size != nil {
			size = int64(*fileContent.Size)
		}
		return &GitHubFileInfo{
			name:  filepath.Base(name),
			size:  size,
			isDir: false,
		}, nil
	}

	return nil, &os.PathError{Op: "stat", Path: name, Err: ErrNotExist}
}

// Chmod is not supported and always returns ErrNotSupported.
// GitHub does not support file permission metadata.
func (fs *Fs) Chmod(name string, mode os.FileMode) error { return ErrNotSupported }

// Chown is not supported and always returns ErrNotSupported.
// GitHub does not support file ownership metadata.
func (fs *Fs) Chown(name string, uid, gid int) error { return ErrNotSupported }

// Chtimes is not supported and always returns ErrNotSupported.
// GitHub does not support access/modification time metadata.
func (fs *Fs) Chtimes(name string, atime, mtime time.Time) error { return ErrNotSupported }

func (fs *Fs) github() *gh.Client {
	return fs.client.client
}
