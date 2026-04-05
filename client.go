package github

import (
	"context"
	"errors"

	gh "github.com/google/go-github/v84/github"
	"github.com/jferrl/go-githubauth"
	"golang.org/x/oauth2"
)

// ClientOptions is a function type that defines the signature
// for configuration options that can be applied to the Client struct.
type ClientOptions func(*Client) error

// Client is a wrapper around the github.Client that provides
// additional functionality and configuration options.
type Client struct {
	ctx    context.Context
	client *gh.Client
}

// NewClient creates a new Client instance with the provided context and options.
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

// WithGithubClient is a ClientOption that sets the github.Client directly on the Client struct.
func WithGithubClient(githubClient *gh.Client) ClientOptions {
	return func(c *Client) error {
		c.client = githubClient
		return nil
	}
}

// WithGithubToken is a ClientOption that configures the Client to use a personal
// access token for authentication with GitHub.
func WithGithubToken(token string) ClientOptions {
	return func(c *Client) error {
		tokenSource := githubauth.NewPersonalAccessTokenSource(token)
		httpClient := oauth2.NewClient(c.ctx, tokenSource)
		c.client = gh.NewClient(httpClient)
		return nil
	}
}

// WithGithubApplication is a ClientOption that configures the Client to use GitHub App authentication.
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
