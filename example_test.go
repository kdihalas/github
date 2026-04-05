package github_test

import (
	"context"
	"io"
	"log"
	"os"
	"time"

	"github.com/kdihalas/github"
	"github.com/spf13/afero"
)

// Example demonstrates the basic workflow: create a Client with a PAT,
// construct an Fs bound to a repository, and read a file using afero idioms.
func Example() {
	// Create a context for API operations.
	ctx := context.Background()

	// Create a Client with a personal access token from the environment.
	// In real use, set GITHUB_TOKEN to your PAT. For this example, we skip network calls.
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return // Skip if no token available.
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	// Create an Fs bound to owner "octocat", repo "hello-world", branch "main".
	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Open and read a file from the repository.
	file, err := fs.Open("README.md")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Read a few bytes to demonstrate the afero.File interface.
	buf := make([]byte, 32)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		log.Fatal(err)
	}

	// Output will vary based on actual repository content, so no output assertion.
	_ = n // bytes read from the file
}

// ExampleNewClient shows how to construct a Client with a GitHub personal access token.
func ExampleNewClient() {
	ctx := context.Background()

	// Construct a Client using a PAT from the environment.
	// The token is optional for public repositories, but required for private ones.
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Println("GITHUB_TOKEN not set; skipping example")
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	// The client is now configured and ready to be used with NewFsFromClient.
	_ = client
}

// ExampleWithGithubApplication demonstrates Client construction with GitHub App credentials.
// GitHub Apps use a clientID, installation ID, and a PEM-encoded private key for authentication.
func ExampleWithGithubApplication() {
	ctx := context.Background()

	// In a real application, you would:
	//   1. Read the private key from a PEM file.
	//   2. Parse the clientID and installationID from your application configuration.
	// For demonstration, we use placeholder values.

	clientID := "112312" // Your GitHub App's client ID
	installationID := int64(12345)

	// In production, load this from a secure location, e.g., a PEM file on disk.
	// Example: privateKey, _ := os.ReadFile("/path/to/private-key.pem")
	// For this example, we use a placeholder to avoid requiring a real key.
	privateKeyPEM := []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA0Z3VS5JJcds... (truncated for brevity)
-----END RSA PRIVATE KEY-----`)

	client, err := github.NewClient(ctx,
		github.WithGithubApplication(clientID, installationID, privateKeyPEM),
	)
	if err != nil {
		// In this example, the private key is invalid, so we expect an error.
		// In production, this would succeed with a valid PEM.
		_ = err
		return
	}

	// The client is now authenticated and ready to use.
	_ = client
}

// ExampleNewFsFromClient demonstrates how to construct an Fs bound to a repository,
// with cache TTL and commit author options.
func ExampleNewFsFromClient() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Println("GITHUB_TOKEN not set; skipping example")
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	// Create an Fs bound to octocat/hello-world on the main branch.
	// Customize the cache TTL to 60 seconds and set a commit author.
	fs := github.NewFsFromClient(
		client,
		"octocat",     // repository owner
		"hello-world", // repository name
		"main",        // branch
		github.WithCacheTTL(60*time.Second),
		github.WithCommitAuthor("Bot User", "bot@example.com"),
	)

	// The Fs is now ready for file operations (Open, Create, Stat, etc.).
	// Writes will be committed with the specified author name and email.
	_ = fs
}

// ExampleFs_Open demonstrates opening a file in read-only mode and reading its content.
func ExampleFs_Open() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Open a file for reading. The file is fetched from GitHub if not cached.
	file, err := fs.Open("README.md")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Read up to 256 bytes from the file.
	buf := make([]byte, 256)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		log.Fatal(err)
	}

	// The data in buf[:n] now contains the file content.
	_ = buf[:n]
}

// ExampleFs_Stat demonstrates checking file metadata and distinguishing files from directories.
func ExampleFs_Stat() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Stat a file to get its metadata.
	info, err := fs.Stat("README.md")
	if err != nil {
		log.Fatal(err)
	}

	// Check if it's a directory or a regular file.
	if info.IsDir() {
		log.Println("It's a directory")
	} else {
		log.Printf("It's a file, size: %d bytes\n", info.Size())
	}
}

// ExampleFs_Create demonstrates creating and writing a new file to the repository.
// The file is committed immediately upon Close().
func ExampleFs_Create() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Create a new file (or truncate if it exists).
	file, err := fs.Create("example.txt")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Write content to the file. The write is buffered.
	content := []byte("Hello, GitHub Filesystem!")
	n, err := file.Write(content)
	if err != nil {
		log.Fatal(err)
	}

	// Upon Close(), the buffered content is flushed and committed to the repository.
	// Bytes written: n
	_ = n
}

// ExampleFs_Mkdir demonstrates creating a directory by creating a .gitkeep placeholder.
func ExampleFs_Mkdir() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Create a directory. Since GitHub has no native directories,
	// a .gitkeep file is created inside the directory to mark its existence.
	err = fs.Mkdir("docs", 0755)
	if err != nil {
		log.Fatal(err)
	}

	// You can now write files to the docs/ directory.
	file, err := fs.Create("docs/api.md")
	if err != nil {
		log.Fatal(err)
	}
	file.Close()
}

// ExampleFile_RangeReader demonstrates efficient byte-range reads using HTTP Range requests.
// This is useful for reading specific portions of large files without downloading the whole content.
func ExampleFile_RangeReader() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Open a file.
	file, err := fs.Open("data.bin")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Use RangeReader to read bytes 100-199 (100 bytes starting at offset 100).
	// This is more efficient than reading the entire file and seeking to the offset.
	// RangeReader is available on the concrete *github.File type.
	ghFile := file.(*github.File)
	rangeReader, err := ghFile.RangeReader(100, 100)
	if err != nil {
		log.Fatal(err)
	}
	defer rangeReader.Close()

	// Read the range data.
	rangeBuf := make([]byte, 100)
	n, err := io.ReadFull(rangeReader, rangeBuf)
	if err != nil && err != io.ErrUnexpectedEOF {
		log.Fatal(err)
	}

	// rangeBuf[:n] contains the requested byte range.
	_ = rangeBuf[:n]
}

// Example_walkDir demonstrates walking a repository subtree using afero.Walk.
// It prints the names and sizes of all files encountered.
func Example_walkDir() {
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return
	}

	client, err := github.NewClient(ctx, github.WithGithubToken(token))
	if err != nil {
		log.Fatal(err)
	}

	fs := github.NewFsFromClient(client, "octocat", "hello-world", "main")

	// Walk the "docs" directory and all its subdirectories.
	// afero.WalkDir mimics filepath.WalkDir and calls the callback for each entry.
	err = afero.Walk(fs, "docs", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err // Stop on error.
		}

		// Print file information.
		if info.IsDir() {
			log.Printf("Dir:  %s\n", path)
		} else {
			log.Printf("File: %s (%d bytes)\n", path, info.Size())
		}

		return nil // Continue walking.
	})

	if err != nil {
		log.Fatal(err)
	}
}
