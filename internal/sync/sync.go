// Package sync pushes local cassettes to the PromptVCR cloud vault via the
// Supabase `upsert_fixture` RPC (which encrypts payloads in Vault server-side).
package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/promptvcr/cli/internal/store"
)

// Client talks to the Supabase REST/RPC endpoint.
type Client struct {
	BaseURL string // e.g. https://<ref>.supabase.co
	APIKey  string // anon or publishable key
	Token   string // user access token (JWT) for RLS
	TeamID  string
	http    *http.Client
}

// New returns a sync client.
func New(baseURL, apiKey, token, teamID string) *Client {
	return &Client{
		BaseURL: baseURL, APIKey: apiKey, Token: token, TeamID: teamID,
		http: &http.Client{Timeout: 30 * time.Second},
	}
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
	args := upsertArgs{
		TeamID: c.TeamID, CacheKey: rec.Key, Name: rec.Key, Provider: rec.Provider,
		Model: rec.Model, Host: rec.Request.Host, Path: rec.Request.Path, TTFTMs: rec.TTFTMs,
		Request: reqJSON, Response: respJSON,
	}
	body, _ := json.Marshal(args)

	url := c.BaseURL + "/rest/v1/rpc/upsert_fixture"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", c.APIKey)
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	res, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := json.Marshal(res.Status)
		return fmt.Errorf("supabase rpc %s: %s", res.Status, string(b))
	}
	return nil
}
