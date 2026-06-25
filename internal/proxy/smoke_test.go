//go:build smoke

// Live, paid smoke tests. Excluded from normal `go test ./...` and from CI by the
// `smoke` build tag; each provider is skipped unless its API key is present.
//
//	OPENAI_API_KEY=...    ANTHROPIC_API_KEY=...    go test -tags smoke ./internal/proxy/ -run Smoke -v
//
// Unlike the in-CI integration test, this exercises the real TLS-MITM path against
// the live provider: the client trusts the PromptVCR CA, the proxy intercepts the
// HTTPS stream, records it, and the second (cache-hit) call must replay it so an
// SSE parser observes identical events. Only the first call per provider is paid.
package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/promptvcr/cli/internal/ca"
	"github.com/promptvcr/cli/internal/config"
	"github.com/promptvcr/cli/internal/sse"
	"github.com/promptvcr/cli/internal/store"
)

func TestSmokeOpenAI(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"say hi in one word"}]}`
	runSmoke(t, "https://api.openai.com/v1/chat/completions", body, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
	})
}

func TestSmokeAnthropic(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	body := `{"model":"claude-3-5-haiku-latest","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"say hi in one word"}]}`
	runSmoke(t, "https://api.anthropic.com/v1/messages", body, func(r *http.Request) {
		r.Header.Set("x-api-key", key)
		r.Header.Set("anthropic-version", "2023-06-01")
		r.Header.Set("Content-Type", "application/json")
	})
}

// runSmoke records a live streamed request through the MITM proxy, then replays it
// and asserts an SSE parser sees byte-identical events. parseSSE/frame are shared
// with proxy_test.go.
func runSmoke(t *testing.T, endpoint, body string, setHeaders func(*http.Request)) {
	t.Helper()

	caDir := t.TempDir()
	if err := ca.Ensure(caDir); err != nil {
		t.Fatalf("ca: %v", err)
	}
	caCert, err := ca.Load(caDir)
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	s, err := New(caDir, st, config.ModeAuto, sse.Instant)
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	proxySrv := httptest.NewServer(s.Handler())
	defer proxySrv.Close()
	proxyURL, _ := url.Parse(proxySrv.URL)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}

	do := func() []byte {
		req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
		setHeaders(req)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("provider returned %d: %s", resp.StatusCode, string(b))
		}
		return b
	}

	live := do()     // miss -> live + record (the only paid call)
	replayed := do() // hit  -> replay from cassette

	liveFrames := parseSSE(live)
	replayFrames := parseSSE(replayed)
	if len(liveFrames) == 0 {
		t.Fatalf("no SSE frames parsed from live stream: %q", live)
	}
	if len(liveFrames) != len(replayFrames) {
		t.Fatalf("frame count differs: live %d vs replay %d", len(liveFrames), len(replayFrames))
	}
	for i := range liveFrames {
		if liveFrames[i] != replayFrames[i] {
			t.Errorf("frame %d differs:\n live   %+v\n replay %+v", i, liveFrames[i], replayFrames[i])
		}
	}

	recs, err := st.List()
	if err != nil || len(recs) != 1 || len(recs[0].Response.Stream) == 0 {
		t.Fatalf("expected exactly 1 cassette with stream chunks, got %d (err %v)", len(recs), err)
	}
	t.Logf("recorded %d stream chunks; replay matched %d frames", len(recs[0].Response.Stream), len(replayFrames))
}
