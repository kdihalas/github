// Package github exposes a GitHub repository as a filesystem implementing [spf13/afero.Fs].
// Reads and writes to the virtual filesystem are translated into GitHub Contents API calls,
// with an HTTP Range request fallback for efficient byte-range reads from raw.githubusercontent.com.
//
// The package provides a Client for GitHub API authentication (supporting personal access tokens,
// GitHub Apps, or direct github.Client) and an Fs type that implements afero.Fs for file operations
// on a repository. All paths are normalized to forward slashes and have leading slashes trimmed.
//
// The Fs type maintains three parallel caches for consistency:
//   - memFs (afero MemMapFs): holds decoded file bytes to avoid repeated API calls
//   - shaCache: path to blob SHA mapping, required by GitHub API for updates/deletes
//   - ttlCache: path to last-fetch time, gated by configurable TTL (default 30s)
//
// Directory operations use GitHub's Contents API list response, creating .gitkeep placeholders
// for directories since GitHub has no native directory support. File operations support read,
// write, append, and seek semantics on top of GitHub's blob-replacement model.
package github

import (
	"context"
	"errors"

	gh "github.com/google/go-github/v84/github"
	"github.com/jferrl/go-githubauth"
	"golang.org/x/oauth2"
)

// ClientOptions is a function type that configures a Client using the functional options pattern.
type ClientOptions func(*Client) error

// Client is a wrapper around a GitHub API client that coordinates authentication
// and context management for Fs operations.
type Client struct {
	ctx    context.Context
	client *gh.Client
}

// NewClient creates a new Client instance with the provided context and applies all provided options.
// It returns a combined error from all failed options, or nil if all succeed.
func NewClient(ctx context.Context, options ...ClientOptions) (*Client, error) {
	var errs []error
	client := &Client{ctx: ctx}
	// Apply each option to the client and collect any errors that occur.
	for _, option := range options {
		if err := option(client); err != nil {
			errs = append(errs, err)
		}
	}
	// If any errors were collected, return a combined error. Otherwise, return the configured client.
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return client, nil
}

// WithGithubClient sets the underlying github.Client directly, bypassing authentication setup.
// This is useful when you already have a configured client from another source.
func WithGithubClient(githubClient *gh.Client) ClientOptions {
	return func(c *Client) error {
		c.client = githubClient
		return nil
	}
}

// WithGithubToken configures authentication using a GitHub personal access token (PAT).
// The token is wrapped in an oauth2.Client and used to create a new github.Client.
func WithGithubToken(token string) ClientOptions {
	return func(c *Client) error {
		tokenSource := githubauth.NewPersonalAccessTokenSource(token)
		httpClient := oauth2.NewClient(c.ctx, tokenSource)
		c.client = gh.NewClient(httpClient)
		return nil
	}
}

// WithGithubApplication configures authentication using GitHub App credentials.
// It requires the app's clientID, the installation ID, and the private key in PEM-encoded bytes.
// The client will use installation tokens for API calls.
func WithGithubApplication(clientID string, installationID int64, privateKey []byte) ClientOptions {
	return func(c *Client) error {
		appTokenSource, err := githubauth.NewApplicationTokenSource(clientID, privateKey)
		if err != nil {
			return err
		}
		installationTokenSource := githubauth.NewInstallationTokenSource(installationID, appTokenSource)
		httpClient := oauth2.NewClient(c.ctx, installationTokenSource)
		c.client = gh.NewClient(httpClient)
		return nil
	}
}
