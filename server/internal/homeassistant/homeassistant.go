// Package homeassistant asks the Home Assistant Supervisor whether a Home
// Assistant username and password are right, for the add-on's opt-in Home
// Assistant login (docs/architecture.md §5). The Supervisor only answers
// yes or no: Homedrive never gets a Home Assistant token, and never stores
// the password.
package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrUnavailable means the Supervisor couldn't check the password, for
// example while Home Assistant restarts.
var ErrUnavailable = errors.New("Home Assistant can't check passwords right now")

type Client struct {
	url, token string
	http       *http.Client
}

// New talks to the Supervisor at url (http://supervisor in the add-on) with
// the add-on's SUPERVISOR_TOKEN.
func New(url, token string) *Client {
	return &Client{url: url, token: token, http: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// CheckPassword reports whether username and password belong to a Home
// Assistant user. The Supervisor answers 401 for wrong ones; anything else
// that isn't 200 means it couldn't check.
func (c *Client) CheckPassword(ctx context.Context, username, password string) (bool, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/auth", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	// The Supervisor compares the content type exactly, so no charset.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supervisor-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized:
		return false, nil
	case http.StatusForbidden:
		return false, fmt.Errorf("%w: the Supervisor refused (is auth_api set in the add-on's config.yaml?)", ErrUnavailable)
	default:
		return false, fmt.Errorf("%w: the Supervisor answered %s", ErrUnavailable, resp.Status)
	}
}
