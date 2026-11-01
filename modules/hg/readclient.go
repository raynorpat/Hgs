package hg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
)

// ReadClient reads Mercurial data from the hgqlserve service.
type ReadClient struct {
	root string
	http *http.Client

	mu          sync.RWMutex
	addr, token string
}

// NewReadClient returns a client for repositories under root.
func NewReadClient(root string) *ReadClient {
	return &ReadClient{
		root: root,
		// The server closes each connection after one request.
		http: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}},
	}
}

// SetEndpoint sets the service address (host:port) and bearer token.
func (c *ReadClient) SetEndpoint(addr, token string) {
	c.mu.Lock()
	c.addr, c.token = addr, token
	c.mu.Unlock()
}

// relative returns the slash path of an absolute path under the root.
func (c *ReadClient) relative(path string) (string, error) {
	rel, err := filepath.Rel(c.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("hg: %q is not under %q", path, c.root)
	}
	return filepath.ToSlash(rel), nil
}

// do sends a request; the context's deadline bounds it. A context error is
// returned as itself, any other transport failure as ErrReadService.
func (c *ReadClient) do(ctx context.Context, method, endpoint string, query url.Values, body []byte) (*http.Response, error) {
	c.mu.RLock()
	addr, token := c.addr, c.token
	c.mu.RUnlock()
	if addr == "" {
		return nil, fmt.Errorf("%w: no endpoint", ErrReadService)
	}
	u := "http://" + addr + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrReadService, err)
	}
	return resp, nil
}

type gqlErrors struct {
	Errors []struct {
		Message    string
		Extensions struct{ Code string }
	}
}

// serviceError maps the first error's code; nil if there are no errors.
func (e gqlErrors) err() error {
	if len(e.Errors) == 0 {
		return nil
	}
	switch e.Errors[0].Extensions.Code {
	case "NOT_FOUND":
		return ErrNotFound
	case "EMPTY_REPOSITORY":
		return ErrEmptyRepository
	case "INVALID_CURSOR":
		return ErrInvalidCursor
	}
	return fmt.Errorf("%w: %s: %s", ErrReadService, e.Errors[0].Extensions.Code, e.Errors[0].Message)
}

// failure turns a non-success response body into an error.
func failure(status int, body []byte) error {
	var e gqlErrors
	if json.Unmarshal(body, &e) == nil {
		if err := e.err(); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: status %d", ErrReadService, status)
}

// Query runs a GraphQL operation and decodes its data into out. The service
// reports field errors with HTTP 200, so errors are checked on every response.
func (c *ReadClient) Query(ctx context.Context, operation, query string, variables map[string]interface{}, out interface{}) error {
	body, err := json.Marshal(map[string]interface{}{"query": query, "variables": variables, "operationName": operation})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, "/gql", nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrReadService, err)
	}
	var env struct {
		gqlErrors
		Data json.RawMessage
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%w: status %d: bad response", ErrReadService, resp.StatusCode)
	}
	if err := env.err(); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return failure(resp.StatusCode, data)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%w: %v", ErrReadService, err)
	}
	return nil
}

// Stream copies a raw endpoint's body to w and reports whether the service
// truncated it. On failure nothing is written.
func (c *ReadClient) Stream(ctx context.Context, endpoint string, params url.Values, w io.Writer) (truncated bool, err error) {
	resp, err := c.do(ctx, http.MethodGet, endpoint, params, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return false, failure(resp.StatusCode, data)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, err
	}
	return resp.Header.Get("X-Truncated") == "true", nil
}

// Close drops a repository from the service's registry. A service that is not
// running holds nothing open, so a failed connection counts as released.
func (c *ReadClient) Close(ctx context.Context, path string) error {
	rel, err := c.relative(path)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, "/close", url.Values{"repo": {rel}}, nil)
	if err != nil {
		if ctx.Err() == nil {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return failure(resp.StatusCode, data)
	}
	return nil
}
