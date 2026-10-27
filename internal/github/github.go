// Package github talks to the GitHub REST API for release creation and
// parses git remote URLs into owner/repo. It is deliberately tiny: one
// endpoint, net/http only.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client creates releases against the GitHub REST API.
type Client struct {
	// HTTP defaults to http.DefaultClient; tests inject an httptest server.
	HTTP *http.Client
	// Base defaults to https://api.github.com.
	Base string
	// Token is the bearer token; empty means unauthenticated (never useful
	// for create, but the request is still attempted so the error is real).
	Token string
}

// releasePayload is the subset of the create-release API gala sends.
type releasePayload struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// CreateRelease creates (or reports the failure of) the release for a tag.
func (c *Client) CreateRelease(owner, repo, tag, name, body string) error {
	base := c.Base
	if base == "" {
		base = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases", strings.TrimRight(base, "/"), owner, repo)

	payload, err := json.Marshal(releasePayload{
		TagName:    tag,
		Name:       name,
		Body:       body,
		Draft:      false,
		Prerelease: false,
	})
	if err != nil {
		return err
	}

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github release for %s: %w", tag, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("github release for %s: %s: %s", tag, resp.Status, strings.TrimSpace(string(msg)))
}
