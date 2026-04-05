package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	gh "github.com/google/go-github/v84/github"
)

// testServer creates a test HTTP server and a *gh.Client pointed at it.
// The mux returned is where test handlers should be registered.
// The server is automatically closed when the test finishes.
func testServer(t *testing.T) (*gh.Client, *http.ServeMux, string) {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ghClient := gh.NewClient(nil)
	u, _ := url.Parse(server.URL + "/")
	ghClient.BaseURL = u
	ghClient.UploadURL = u

	return ghClient, mux, server.URL
}

// newTestFs creates a *Fs backed by the test server, pointed at owner/repo/branch.
func newTestFs(t *testing.T, owner, repo, branch string, opts ...Option) (*Fs, *http.ServeMux, string) {
	t.Helper()

	ghClient, mux, serverURL := testServer(t)

	client := &Client{
		ctx:    context.Background(),
		client: ghClient,
	}

	fsys := NewFsFromClient(client, owner, repo, branch, opts...)
	return fsys, mux, serverURL
}

// b64 encodes a string once with standard base64.
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// fileContentJSON returns JSON for a single file GitHub Contents API response.
//
// fetchFile calls GetContent() (which base64-decodes the content field once),
// then decodes the result a second time. So the JSON "content" field must hold
// base64(base64(actualContent)) so that after two decode passes we get the
// original bytes back.
func fileContentJSON(path, content, sha string, size int) string {
	doubleEncoded := b64(b64(content))
	return fmt.Sprintf(`{
		"type": "file",
		"name": %q,
		"path": %q,
		"sha": %q,
		"size": %d,
		"content": %q,
		"encoding": "base64"
	}`, baseName(path), path, sha, size, doubleEncoded)
}

// dirContentJSON returns JSON for a directory GitHub Contents API response.
func dirContentJSON(entries []dirEntrySpec) string {
	items := make([]string, len(entries))
	for i, e := range entries {
		typ := "file"
		if e.IsDir {
			typ = "dir"
		}
		items[i] = fmt.Sprintf(`{"type":%q,"name":%q,"path":%q,"sha":%q,"size":%d}`,
			typ, baseName(e.Path), e.Path, e.SHA, e.Size)
	}
	b, _ := json.Marshal(json.RawMessage("[" + joinStrings(items) + "]"))
	return string(b)
}

type dirEntrySpec struct {
	Path  string
	SHA   string
	Size  int
	IsDir bool
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// respondNotFound writes a 404 GitHub error response.
func respondNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest"}`)
}

// respondError writes a non-404 GitHub error response.
func respondError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"message":%q}`, msg)
}

// updateFileResponseJSON returns JSON for a file update/create response.
func updateFileResponseJSON(path, sha string) string {
	return fmt.Sprintf(`{
		"content": {
			"type": "file",
			"name": %q,
			"path": %q,
			"sha": %q,
			"size": 10
		},
		"commit": {"sha": "abc123"}
	}`, baseName(path), path, sha)
}

// shortTTLOpt returns a WithCacheTTL option set to a very short duration for cache-expiry tests.
func shortTTLOpt() Option {
	return WithCacheTTL(1 * time.Millisecond)
}

// registerGetContents registers a handler at /repos/{owner}/{repo}/contents/{path}
// that returns the given body. The path param should be URL-relative like "docs/file.txt".
func registerGetContents(mux *http.ServeMux, owner, repo, filePath, body string) {
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/contents/"+filePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
}

// registerPutContents registers a handler for PUT /repos/{owner}/{repo}/contents/{path}.
func registerPutContents(mux *http.ServeMux, owner, repo, filePath, body string) {
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/contents/"+filePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
}

// strPtr returns a pointer to the given string value.
func strPtr(s string) *string { return &s }

// registerDeleteContents registers a handler for DELETE /repos/{owner}/{repo}/contents/{path}.
func registerDeleteContents(mux *http.ServeMux, owner, repo, filePath, body string) {
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/contents/"+filePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			fmt.Fprint(w, body)
		} else {
			respondNotFound(w)
		}
	})
}
