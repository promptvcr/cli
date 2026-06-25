// Package sync pushes local cassettes to the PromptVCR cloud vault via the
// Supabase `upsert_fixture` RPC (which encrypts payloads in Vault server-side).
//
// Two auth modes are supported:
//   - Legacy JWT: env-based flow using a user access token (JWT) + team id,
//     calling `upsert_fixture` with an Authorization bearer header.
//   - Token (PAT): the dashboard-minted `pvcr_<hex>` access token passed as a
//     function argument to the token-authed `cli_*` RPCs. Only the `apikey`
//     header is sent (no Authorization bearer).
package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/promptvcr/cli/internal/store"
)

// Client talks to the Supabase REST/RPC endpoint.
type Client struct {
	BaseURL string // e.g. https://<ref>.supabase.co
	APIKey  string // anon or publishable key
	Token   string // user access token (JWT) or PromptVCR PAT (pvcr_<hex>)
	TeamID  string
	http    *http.Client
}

// New returns a sync client for the legacy JWT/team flow.
func New(baseURL, apiKey, token, teamID string) *Client {
	return &Client{
		BaseURL: baseURL, APIKey: apiKey, Token: token, TeamID: teamID,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithToken returns a sync client authenticated with a PromptVCR PAT. The
// token is passed as an RPC argument and only the apikey header is sent.
func NewWithToken(baseURL, apiKey, token string) *Client {
	return &Client{
		BaseURL: baseURL, APIKey: apiKey, Token: token,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// usesToken reports whether the client should use the token-authed cli_* RPCs.
func (c *Client) usesToken() bool {
	return strings.HasPrefix(c.Token, "pvcr_") && c.TeamID == ""
}

type upsertArgs struct {
	TeamID   string          `json:"p_team_id"`
	CacheKey string          `json:"p_cache_key"`
	Name     string          `json:"p_name"`
	Provider string          `json:"p_provider"`
	Model    string          `json:"p_model"`
	Host     string          `json:"p_host"`
	Path     string          `json:"p_path"`
	TTFTMs   int64           `json:"p_ttft_ms"`
	Request  json.RawMessage `json:"p_request"`
	Response json.RawMessage `json:"p_response"`
}

type cliUpsertArgs struct {
	Token    string          `json:"p_token"`
	CacheKey string          `json:"p_cache_key"`
	Name     string          `json:"p_name"`
	Provider string          `json:"p_provider"`
	Model    string          `json:"p_model"`
	Host     string          `json:"p_host"`
	Path     string          `json:"p_path"`
	TTFTMs   int64           `json:"p_ttft_ms"`
	Request  json.RawMessage `json:"p_request"`
	Response json.RawMessage `json:"p_response"`
}

// CloudFixture is one row returned by cli_list_fixtures.
type CloudFixture struct {
	CacheKey   string `json:"cache_key"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Watch      bool   `json:"watch"`
	RecordedAt string `json:"recorded_at"`
	UpdatedAt  string `json:"updated_at"`
}

// PulledFixture is the JSON object returned by cli_pull_fixture.
type PulledFixture struct {
	CacheKey string          `json:"cache_key"`
	Name     string          `json:"name"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Host     string          `json:"host"`
	Path     string          `json:"path"`
	TTFTMs   int64           `json:"ttft_ms"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

// PushAll uploads every local record, returning the number pushed.
func (c *Client) PushAll(records []*store.Record) (int, error) {
	n := 0
	for _, rec := range records {
		if err := c.push(rec); err != nil {
			return n, fmt.Errorf("push %s: %w", rec.Key, err)
		}
		n++
	}
	return n, nil
}

func (c *Client) push(rec *store.Record) error {
	reqJSON, _ := json.Marshal(rec.Request)
	respJSON, _ := json.Marshal(rec.Response)

	if c.usesToken() {
		args := cliUpsertArgs{
			Token: c.Token, CacheKey: rec.Key, Name: rec.Key, Provider: rec.Provider,
			Model: rec.Model, Host: rec.Request.Host, Path: rec.Request.Path, TTFTMs: rec.TTFTMs,
			Request: reqJSON, Response: respJSON,
		}
		_, err := c.rpc("cli_upsert_fixture", args)
		return err
	}

	args := upsertArgs{
		TeamID: c.TeamID, CacheKey: rec.Key, Name: rec.Key, Provider: rec.Provider,
		Model: rec.Model, Host: rec.Request.Host, Path: rec.Request.Path, TTFTMs: rec.TTFTMs,
		Request: reqJSON, Response: respJSON,
	}
	_, err := c.rpc("upsert_fixture", args)
	return err
}

// ListCloud returns the fixtures visible to the authenticated token.
func (c *Client) ListCloud() ([]CloudFixture, error) {
	body, err := c.rpc("cli_list_fixtures", map[string]string{"p_token": c.Token})
	if err != nil {
		return nil, err
	}
	var out []CloudFixture
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode cli_list_fixtures: %w", err)
	}
	return out, nil
}

// Pull fetches a single fixture by cache key.
func (c *Client) Pull(cacheKey string) (*PulledFixture, error) {
	body, err := c.rpc("cli_pull_fixture", map[string]string{
		"p_token": c.Token, "p_cache_key": cacheKey,
	})
	if err != nil {
		return nil, err
	}
	var out PulledFixture
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode cli_pull_fixture: %w", err)
	}
	return &out, nil
}

// rpc posts args to /rest/v1/rpc/<fn> and returns the response body. The
// Authorization bearer header is only sent for the legacy JWT flow; token-authed
// cli_* RPCs receive the token as an argument and need only the apikey header.
func (c *Client) rpc(fn string, args any) ([]byte, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	url := c.BaseURL + "/rest/v1/rpc/" + fn
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", c.APIKey)
	if !c.usesToken() {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("supabase rpc %s: %s: %s", fn, res.Status, strings.TrimSpace(buf.String()))
	}
	return buf.Bytes(), nil
}
