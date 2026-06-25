package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/promptvcr/cli/internal/ca"
	"github.com/promptvcr/cli/internal/config"
	"github.com/promptvcr/cli/internal/sse"
	"github.com/promptvcr/cli/internal/store"
)

// These streams use the exact framing real providers emit: OpenAI is data-only
// terminated by `data: [DONE]`; Anthropic uses named `event:` lines and ends
// with `event: message_stop` (no [DONE]).
const openAIStream = "data: {\"id\":\"cmpl_1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: [DONE]\n\n"

const anthropicStream = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"role\":\"assistant\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

type frame struct{ event, data string }

// parseSSE extracts (event, data) tuples the way any SSE client / provider SDK
// would, so we can assert the replayed stream is observationally identical.
func parseSSE(b []byte) []frame {
	var out []frame
	var ev string
	var data []string
	flush := func() {
		if len(data) > 0 {
			out = append(out, frame{ev, strings.Join(data, "\n")})
		}
		ev, data = "", nil
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, val := line, ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			field, val = line[:i], strings.TrimPrefix(line[i+1:], " ")
		}
		switch field {
		case "event":
			ev = val
		case "data":
			data = append(data, val)
		}
	}
	flush()
	return out
}

// TestProxyRecordReplayRoundTrip is the end-to-end proof of the headline claim:
// a streamed provider response, captured by the proxy, replays byte-faithfully so
// an SDK parsing the replay observes the exact same events (names + payloads).
//
// It routes canonical OpenAI/Anthropic SSE through the real goproxy + record +
// replay path. Live API keys can't run in CI, so the upstream is a local server
// emitting the providers' exact wire framing; provider DNS is redirected to it.
func TestProxyRecordReplayRoundTrip(t *testing.T) {
	cases := []struct {
		name, host, stream string
	}{
		{"openai", "api.openai.com", openAIStream},
		{"anthropic", "api.anthropic.com", anthropicStream},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, tc.stream)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}))
			defer upstream.Close()
			upHostPort := strings.TrimPrefix(upstream.URL, "http://")

			caDir := t.TempDir()
			if err := ca.Ensure(caDir); err != nil {
				t.Fatalf("ca: %v", err)
			}
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatalf("store: %v", err)
			}
			s, err := New(caDir, st, config.ModeAuto, sse.Instant)
			if err != nil {
				t.Fatalf("new proxy: %v", err)
			}
			// Redirect the provider host to our local upstream (no real network).
			s.proxy.Tr = &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if strings.HasPrefix(addr, tc.host+":") {
						addr = upHostPort
					}
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			}

			proxySrv := httptest.NewServer(s.Handler())
			defer proxySrv.Close()
			proxyURL, _ := url.Parse(proxySrv.URL)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

			const body = `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`
			do := func() []byte {
				req, _ := http.NewRequest(http.MethodPost, "http://"+tc.host+"/v1/messages", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				defer resp.Body.Close()
				b, _ := io.ReadAll(resp.Body)
				return b
			}

			live := do()     // miss -> proxied to upstream + recorded
			replayed := do() // hit  -> served from the cassette

			liveFrames := parseSSE(live)
			replayFrames := parseSSE(replayed)
			if len(liveFrames) == 0 {
				t.Fatalf("no frames parsed from live stream: %q", live)
			}
			if len(liveFrames) != len(replayFrames) {
				t.Fatalf("frame count differs: live %d vs replay %d\nlive=%q\nreplay=%q",
					len(liveFrames), len(replayFrames), live, replayed)
			}
			for i := range liveFrames {
				if liveFrames[i] != replayFrames[i] {
					t.Errorf("frame %d differs:\n live   %+v\n replay %+v", i, liveFrames[i], replayFrames[i])
				}
			}

			// The replay must not invent an OpenAI-style [DONE] for Anthropic.
			if tc.name == "anthropic" && strings.Contains(string(replayed), "[DONE]") {
				t.Errorf("anthropic replay should not contain a synthetic [DONE]:\n%q", replayed)
			}

			// The recorded cassette preserved SSE event names for Anthropic.
			recs, err := st.List()
			if err != nil || len(recs) != 1 {
				t.Fatalf("expected exactly 1 cassette, got %d (err %v)", len(recs), err)
			}
			if tc.name == "anthropic" {
				var sawEvent bool
				for _, c := range recs[0].Response.Stream {
					if c.Event == "content_block_delta" {
						sawEvent = true
					}
				}
				if !sawEvent {
					t.Errorf("recorded Anthropic cassette lost its event names: %+v", recs[0].Response.Stream)
				}
			}
		})
	}
}
