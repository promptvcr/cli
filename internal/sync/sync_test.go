package sync

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/promptvcr/cli/internal/store"
)

func TestPushWithTokenUsesCliRPC(t *testing.T) {
	var gotPath, gotAPIKey, gotAuth string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("apikey")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"00000000-0000-0000-0000-000000000000"`))
	}))
	defer srv.Close()

	rec := &store.Record{
		Key:      "abc123",
		Provider: "openai",
		Model:    "gpt-4o",
		Request:  store.Request{Method: "POST", Host: "api.openai.com", Path: "/v1/chat/completions"},
		Response: store.Response{Status: 200},
		TTFTMs:   42,
	}

	n, err := NewWithToken(srv.URL, "anon-key", "pvcr_token").PushAll([]*store.Record{rec})
	if err != nil {
		t.Fatalf("PushAll() = %v", err)
	}
	if n != 1 {
		t.Errorf("pushed %d, want 1", n)
	}
	if gotPath != "/rest/v1/rpc/cli_upsert_fixture" {
		t.Errorf("path = %q, want /rest/v1/rpc/cli_upsert_fixture", gotPath)
	}
	if gotAPIKey != "anon-key" {
		t.Errorf("apikey = %q, want anon-key", gotAPIKey)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty for token push", gotAuth)
	}
	if body["p_token"] != "pvcr_token" {
		t.Errorf("p_token = %v, want pvcr_token", body["p_token"])
	}
	if body["p_cache_key"] != "abc123" {
		t.Errorf("p_cache_key = %v, want abc123", body["p_cache_key"])
	}
}

func TestPushLegacyUsesJWTAndUpsertFixture(t *testing.T) {
	var gotPath, gotAuth string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := &store.Record{Key: "k1", Provider: "openai", Request: store.Request{Host: "api.openai.com"}}
	if _, err := New(srv.URL, "anon-key", "jwt-token", "team-1").PushAll([]*store.Record{rec}); err != nil {
		t.Fatalf("PushAll() = %v", err)
	}
	if gotPath != "/rest/v1/rpc/upsert_fixture" {
		t.Errorf("path = %q, want /rest/v1/rpc/upsert_fixture", gotPath)
	}
	if gotAuth != "Bearer jwt-token" {
		t.Errorf("Authorization = %q, want Bearer jwt-token", gotAuth)
	}
	if body["p_team_id"] != "team-1" {
		t.Errorf("p_team_id = %v, want team-1", body["p_team_id"])
	}
}

func TestListCloudParsesRows(t *testing.T) {
	var gotPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write([]byte(`[
			{"cache_key":"k1","name":"k1","provider":"openai","model":"gpt-4o","watch":true,"recorded_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"},
			{"cache_key":"k2","name":"k2","provider":"anthropic","model":"claude","watch":false,"recorded_at":"2026-01-03T00:00:00Z","updated_at":"2026-01-04T00:00:00Z"}
		]`))
	}))
	defer srv.Close()

	rows, err := NewWithToken(srv.URL, "anon-key", "pvcr_token").ListCloud()
	if err != nil {
		t.Fatalf("ListCloud() = %v", err)
	}
	if gotPath != "/rest/v1/rpc/cli_list_fixtures" {
		t.Errorf("path = %q, want cli_list_fixtures", gotPath)
	}
	if body["p_token"] != "pvcr_token" {
		t.Errorf("p_token = %v, want pvcr_token", body["p_token"])
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].CacheKey != "k1" || rows[0].Provider != "openai" || !rows[0].Watch {
		t.Errorf("row[0] = %+v unexpected", rows[0])
	}
	if rows[1].Model != "claude" || rows[1].Watch {
		t.Errorf("row[1] = %+v unexpected", rows[1])
	}
}

func TestPullParsesFixture(t *testing.T) {
	var gotPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write([]byte(`{
			"cache_key":"k1","name":"k1","provider":"openai","model":"gpt-4o",
			"host":"api.openai.com","path":"/v1/chat/completions","ttft_ms":12,
			"request":{"method":"POST","host":"api.openai.com","path":"/v1/chat/completions"},
			"response":{"status":200,"headers":{}}
		}`))
	}))
	defer srv.Close()

	pf, err := NewWithToken(srv.URL, "anon-key", "pvcr_token").Pull("k1")
	if err != nil {
		t.Fatalf("Pull() = %v", err)
	}
	if gotPath != "/rest/v1/rpc/cli_pull_fixture" {
		t.Errorf("path = %q, want cli_pull_fixture", gotPath)
	}
	if body["p_cache_key"] != "k1" || body["p_token"] != "pvcr_token" {
		t.Errorf("body = %v, want p_cache_key=k1 p_token=pvcr_token", body)
	}
	if pf.CacheKey != "k1" || pf.Provider != "openai" || pf.TTFTMs != 12 {
		t.Errorf("pf = %+v unexpected", pf)
	}

	var req store.Request
	if err := json.Unmarshal(pf.Request, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.Path != "/v1/chat/completions" {
		t.Errorf("request path = %q, want /v1/chat/completions", req.Path)
	}
}
