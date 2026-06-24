package sse

import (
	"strings"
	"testing"
	"time"

	"github.com/promptvcr/cli/internal/store"
)

func TestExtractData(t *testing.T) {
	cases := []struct {
		line   string
		want   string
		wantOK bool
	}{
		{"data: {\"a\":1}", "{\"a\":1}", true},
		{"data:{\"a\":1}", "{\"a\":1}", true},
		{"{\"ndjson\":true}", "{\"ndjson\":true}", true},
		{": comment", "", false},
		{"event: ping", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := extractData(c.line)
		if ok != c.wantOK || got != c.want {
			t.Errorf("extractData(%q) = (%q,%v), want (%q,%v)", c.line, got, ok, c.want, c.wantOK)
		}
	}
}

func TestReplayInstantWritesAllChunksThenDone(t *testing.T) {
	chunks := []store.Chunk{
		{OffsetMs: 100, Data: "{\"i\":0}"},
		{OffsetMs: 200, Data: "{\"i\":1}"},
	}
	var sb strings.Builder
	start := time.Now()
	Replay(&sb, nil, chunks, Instant, 0)
	elapsed := time.Since(start)

	want := "data: {\"i\":0}\n\ndata: {\"i\":1}\n\ndata: [DONE]\n\n"
	if sb.String() != want {
		t.Fatalf("unexpected replay output:\n got %q\n want %q", sb.String(), want)
	}
	// Instant mode must not honor the recorded offsets.
	if elapsed > 50*time.Millisecond {
		t.Fatalf("instant replay should not sleep, took %v", elapsed)
	}
}

func TestReplayAcceleratedIsFasterThanRealtime(t *testing.T) {
	chunks := []store.Chunk{{OffsetMs: 0, Data: "a"}, {OffsetMs: 120, Data: "b"}}
	var sb strings.Builder
	start := time.Now()
	Replay(&sb, nil, chunks, Accelerated, 10) // 120ms / 10 = ~12ms
	if elapsed := time.Since(start); elapsed > 80*time.Millisecond {
		t.Fatalf("accelerated replay too slow: %v", elapsed)
	}
}
