// Package admincli is the hs-admin operator client: typed calls against the
// hs-controller API plus pure command-line builders for the official
// tailscale-ssh transport. The builders are pure so tests pin them
// byte-for-byte; the client is httptest-backed.
package admincli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to hs-controller with the admin bearer token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(base, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// TokenFromSources resolves the admin token: flag > env > the 0600 file
// hs-controller init writes. Empty everywhere = "" (caller decides whether
// that is fatal).
func TokenFromSources(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("DIALHOME_HS_ADMIN_TOKEN"); v != "" {
		return strings.TrimSpace(v)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(home + "/.dialhome-hs/admin-token"); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func (c *Client) do(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// ErrUnauthorized marks a 401 so the CLI can say "token missing/wrong".
var ErrUnauthorized = errors.New("admin token missing or rejected")

// Node mirrors the store record fields the CLI renders.
type Node struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Hostname string `json:"hostname"`
	Profile  string `json:"profile"`
}

// Invitation mirrors the store invitation fields the CLI renders.
type Invitation struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Label     string `json:"label"`
	Profile   string `json:"profile"`
	ExpiresAt string `json:"expires_at"`
}

type nodesResponse struct {
	Nodes       []*Node       `json:"nodes"`
	Invitations []*Invitation `json:"invitations"`
}

// ListNodes returns (nodes, invitations).
func (c *Client) ListNodes() ([]*Node, []*Invitation, error) {
	code, b, err := c.do(http.MethodGet, "/api/v1/nodes", nil)
	if err != nil {
		return nil, nil, err
	}
	if code == 401 {
		return nil, nil, ErrUnauthorized
	}
	if code != 200 {
		return nil, nil, fmt.Errorf("GET /nodes: HTTP %d: %s", code, b)
	}
	var out nodesResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, nil, err
	}
	return out.Nodes, out.Invitations, nil
}

// CreateInvite mints a one-time invitation; the code is in the response body
// and shown exactly once.
func (c *Client) CreateInvite(label, profile string, ttlHours float64) (code string, id string, err error) {
	st, b, err := c.do(http.MethodPost, "/api/v1/invitations", map[string]any{
		"label": label, "profile": profile, "ttl_hours": ttlHours,
	})
	if err != nil {
		return "", "", err
	}
	if st == 401 {
		return "", "", ErrUnauthorized
	}
	if st != 201 {
		return "", "", fmt.Errorf("POST /invitations: HTTP %d: %s", st, b)
	}
	var out struct {
		Code string `json:"code"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", "", err
	}
	return out.Code, out.ID, nil
}

// RevokeInvite invalidates an unused invitation.
func (c *Client) RevokeInvite(id string) error {
	st, b, err := c.do(http.MethodDelete, "/api/v1/invitations/"+id, nil)
	if err != nil {
		return err
	}
	if st == 401 {
		return ErrUnauthorized
	}
	if st != 200 {
		return fmt.Errorf("DELETE /invitations/%s: HTTP %d: %s", id, st, b)
	}
	return nil
}

// RevokeNode tombstones a node and expires it in headscale.
func (c *Client) RevokeNode(id string) error {
	st, b, err := c.do(http.MethodPost, "/api/v1/nodes/"+id+"/revoke", nil)
	if err != nil {
		return err
	}
	if st == 401 {
		return ErrUnauthorized
	}
	if st != 200 {
		return fmt.Errorf("POST /nodes/%s/revoke: HTTP %d: %s", id, st, b)
	}
	return nil
}

// Health pings the management plane.
func (c *Client) Health() (string, error) {
	st, b, err := c.do(http.MethodGet, "/api/v1/health", nil)
	if err != nil {
		return "", err
	}
	if st != 200 {
		return "", fmt.Errorf("GET /health: HTTP %d: %s", st, b)
	}
	return string(b), nil
}
