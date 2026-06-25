package sse

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/promptvcr/cli/internal/store"
)

// readAll drives a Recorder to EOF and returns the captured chunks.
func recordStream(t *testing.T, raw string) []store.Chunk {
	t.Helper()
	var got []store.Chunk
	rec := NewRecorder(io.NopCloser(strings.NewReader(raw)), func(c []store.Chunk) { got = c })
	if _, err := io.Copy(io.Discard, rec); err != nil {
		t.Fatalf("copy: %v", err)
	}
	return got
}

func TestRecorderCapturesOpenAIDataFrames(t *testing.T) {
	raw := "data: {\"i\":0}\n\ndata: {\"i\":1}\n\ndata: [DONE]\n\n"
	got := recordStream(t, raw)
	want := []string{"{\"i\":0}", "{\"i\":1}", "[DONE]"}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Data != w || got[i].Event != "" {
			t.Errorf("chunk %d = (%q,%q), want data %q, no event", i, got[i].Event, got[i].Data, w)
		}
	}
}

func TestRecorderCapturesAnthropicEventNames(t *testing.T) {
	raw := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	got := recordStream(t, raw)
	if len(got) != 2 {
		t.Fatalf("got %d chunks, want 2: %+v", len(got), got)
	}
	if got[0].Event != "content_block_delta" {
		t.Errorf("frame 0 event = %q, want content_block_delta", got[0].Event)
	}
	if got[1].Event != "message_stop" {
		t.Errorf("frame 1 event = %q, want message_stop", got[1].Event)
	}
}

func TestRecorderCapturesNDJSON(t *testing.T) {
	raw := "{\"response\":\"a\",\"done\":false}\n{\"response\":\"b\",\"done\":true}\n"
	got := recordStream(t, raw)
	if len(got) != 2 || got[0].Event != "" || got[1].Data != "{\"response\":\"b\",\"done\":true}" {
		t.Fatalf("unexpected NDJSON capture: %+v", got)
	}
}

func TestReplayRoundTripsRecordedFrames(t *testing.T) {
	// Anthropic-style stream survives a record -> replay round trip verbatim.
	raw := "event: content_block_delta\ndata: {\"i\":0}\n\nevent: message_stop\ndata: {\"i\":1}\n\n"
	chunks := recordStream(t, raw)
	var sb strings.Builder
	Replay(&sb, nil, chunks, Instant, 0, false)
	if sb.String() != raw {
		t.Fatalf("round trip mismatch:\n got %q\n want %q", sb.String(), raw)
	}
}

func TestReplayNDJSONEmitsBareLines(t *testing.T) {
	chunks := []store.Chunk{{OffsetMs: 0, Data: "{\"a\":1}"}, {OffsetMs: 5, Data: "{\"b\":2}"}}
	var sb strings.Builder
	Replay(&sb, nil, chunks, Instant, 0, true)
	if want := "{\"a\":1}\n{\"b\":2}\n"; sb.String() != want {
		t.Fatalf("ndjson replay:\n got %q\n want %q", sb.String(), want)
	}
}

func TestReplayInstantDoesNotSleep(t *testing.T) {
	chunks := []store.Chunk{{OffsetMs: 100, Data: "{\"i\":0}"}, {OffsetMs: 200, Data: "{\"i\":1}"}}
	var sb strings.Builder
	start := time.Now()
	Replay(&sb, nil, chunks, Instant, 0, false)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("instant replay should not sleep, took %v", elapsed)
	}
}

func TestReplayAcceleratedIsFasterThanRealtime(t *testing.T) {
	chunks := []store.Chunk{{OffsetMs: 0, Data: "a"}, {OffsetMs: 120, Data: "b"}}
	var sb strings.Builder
	start := time.Now()
	Replay(&sb, nil, chunks, Accelerated, 10, false) // 120ms / 10 = ~12ms
	if elapsed := time.Since(start); elapsed > 80*time.Millisecond {
		t.Fatalf("accelerated replay too slow: %v", elapsed)
	}
}
