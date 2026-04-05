# Github Backend for Afero

A Go `afero.Fs` backed by a GitHub repository. Read, write, delete, and rename files in a repo using standard Go file-IO semantics; under the hood it calls the GitHub Contents API and raw.githubusercontent.com for byte ranges.

## Features

- **afero.Fs-compatible**: Drop-in replacement for any code using `github.com/spf13/afero.Fs`
- **Multiple auth modes**: Personal Access Token, GitHub App (private key + installation ID), or bring your own `go-github` client
- **Cached reads**: TTL-based in-memory caching (default 30s) reduces redundant API calls
- **Deferred writes**: All modifications are buffered; a single GitHub API call per `Close()` means one commit per file flush
- **Partial reads via RangeReader**: Fetch byte ranges efficiently using HTTP Range requests against raw.githubusercontent.com without downloading the entire file
- **WalkDir helper**: Tree traversal with `fs.SkipDir` / `fs.SkipAll` support mimics standard `fs.WalkDir` semantics

## Installation

```bash
go get github.com/kdihalas/github
```

Requires Go 1.25.0 or later.

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/kdihalas/github"
)

func main() {
	client, err := github.NewClient(
		context.Background(),
		github.WithGithubToken(os.Getenv("GITHUB_TOKEN")),
	)
	if err != nil {
		log.Fatalf("Failed to create GitHub client: %v", err)
	}

	fs := github.NewFsFromClient(client, "owner", "repo", "main")

	// Create and write a file
	f, err := fs.Create("config/app.json")
	if err != nil {
		log.Fatal("create:", err)
	}
	_, err = f.WriteString(`{"env": "production"}`)
	if err != nil {
		log.Fatal("write:", err)
	}
	// Single GitHub API call happens here
	if err = f.Close(); err != nil {
		log.Fatal("close:", err)
	}
	fmt.Println("✓ wrote config/app.json")
}
```

## Authentication

### Personal Access Token

```go
client, err := github.NewClient(
	context.Background(),
	github.WithGithubToken(os.Getenv("GITHUB_TOKEN")),
)
```

### GitHub App (Private Key + Installation ID)

```go
privateKey := []byte(`-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----`)

client, err := github.NewClient(
	context.Background(),
	github.WithGithubApplication("your-app-id", 12345, privateKey),
)
```

### Bring Your Own go-github Client

```go
import gh "github.com/google/go-github/v84/github"

myGithubClient := gh.NewClient(httpClient)
client, err := github.NewClient(
	context.Background(),
	github.WithGithubClient(myGithubClient),
)
```

## Usage Patterns

### Reading a File

```go
f, err := fs.Open("path/to/file.txt")
if err != nil {
	log.Fatal(err)
}
defer f.Close()

content, err := io.ReadAll(f)
```

### Listing a Directory

```go
dir, err := fs.Open("path/to/dir")
if err != nil {
	log.Fatal(err)
}
defer dir.Close()

ghDir := dir.(*github.File)
entries, err := ghDir.ReaddirAll()
if err != nil {
	log.Fatal(err)
}
for _, entry := range entries {
	fmt.Printf("%s (dir=%v)\n", entry.Name(), entry.IsDir())
}
```

### Walking a Tree

```go
err := afero.Walk(fs, ".", func(path string, fi os.FileInfo, err error) error {
	if err != nil {
		return err
	}
	fmt.Printf("%s (dir=%v, size=%d)\n", path, fi.IsDir(), fi.Size())
	return nil
})
```

### Partial Reads with RangeReader

```go
f, err := fs.Open("large-file.bin")
if err != nil {
	log.Fatal(err)
}
defer f.Close()

ghFile := f.(*github.File)
rc, err := ghFile.RangeReader(1024, 512) // 512 bytes starting at offset 1024
if err != nil {
	log.Fatal(err)
}
defer rc.Close()

chunk := make([]byte, 512)
n, _ := io.ReadFull(rc, chunk)
fmt.Printf("Read %d bytes\n", n)
```

### Configuring TTL and Commit Author

```go
fs := github.NewFsFromClient(
	client,
	"owner", "repo", "main",
	github.WithCacheTTL(60 * time.Second),        // Cache for 60s instead of 30s
	github.WithAPITimeout(30 * time.Second),      // Per-request timeout
	github.WithCommitAuthor("Bot", "bot@example.com"), // Author on commits
)
```

## How It Works

**GitHub has no directories.** When you call `Mkdir`, the library creates a `.gitkeep` placeholder file. `Stat` returns directory metadata by checking whether the GitHub Contents API returns a single file or a slice of items.

**Writes are deferred.** Calls to `Write` only touch an in-memory buffer. The actual GitHub API call (via the Contents API) happens when you call `Close()` or `Sync()`. Each flush produces exactly one commit.

**Reads are cached.** A three-layer cache minimizes API calls:
- `memFs` (in-memory file content): Stores fetched blobs
- `shaCache` (SHA lookup): Maps paths to their blob SHAs (needed for updates and deletes)
- `ttlCache` (TTL gating): Tracks when each path was last fetched; entries older than the TTL are re-fetched

Default cache TTL is 30 seconds; configure with `WithCacheTTL`.

**Range requests.** `RangeReader` uses HTTP Range requests against raw.githubusercontent.com for efficient partial reads. If the requested range is already in the in-memory buffer, it's served directly without a network call.

## Limitations

- **No real directories**: GitHub stores only files. `Mkdir` creates a `.gitkeep` placeholder; empty directories cannot be stored.
- **No permission metadata**: `Chmod`, `Chown`, and `Chtimes` return `ErrNotSupported`. GitHub does not track file permissions or timestamps.
- **One commit per Close**: Every file flush produces a separate commit. Bulk updates result in N commits, not 1.
- **Size limit**: Files larger than GitHub's Contents API size limit (~100 MB) are not supported.
- **No atomic transactions**: Individual file operations are atomic, but sequences of operations (e.g., "write file A then file B") are not transactional.

## Error Sentinels

The library exports error sentinels for type-safe error handling:

- `ErrNotExist` — file or directory does not exist
- `ErrExist` — file or directory already exists
- `ErrNotSupported` — operation not supported by GitHub (e.g., `Chmod`)
- `ErrNotImplemented` — operation not (yet) implemented
- `ErrAlreadyOpened` — file is already open for reading/writing
- `ErrInvalidSeek` — invalid seek offset

Errors are wrapped in `*os.PathError` at method boundaries for standard Go error handling.

## Dependencies

- `github.com/google/go-github/v84` — GitHub API client
- `github.com/jferrl/go-githubauth` — GitHub authentication helpers
- `github.com/spf13/afero` — Abstract filesystem interface
- `golang.org/x/oauth2` — OAuth2 token management

## License

Copyright (c) 2026 Kostas Dihalas

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
