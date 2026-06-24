// Package sse records and replays Server-Sent Events / NDJSON streams with
// per-chunk timing fidelity.
package sse

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/promptvcr/cli/internal/store"
)

// TimingMode controls how recorded inter-chunk delays are reproduced on replay.
type TimingMode string

const (
	Realtime    TimingMode = "realtime"    // faithful TTFT + inter-chunk gaps
	Instant     TimingMode = "instant"     // back-to-back (CI default)
	Accelerated TimingMode = "accelerated" // N x faster
)

// Recorder wraps an upstream stream body, forwarding bytes to the client
// unchanged while capturing each data chunk with its offset from the first byte.
type Recorder struct {
	src    io.ReadCloser
	reader *bufio.Reader
	start  time.Time
	chunks []store.Chunk
	once   sync.Once
	onDone func([]store.Chunk)
	buf    strings.Builder
}

// NewRecorder tees src, invoking onDone with the captured chunks at EOF.
func NewRecorder(src io.ReadCloser, onDone func([]store.Chunk)) *Recorder {
	return &Recorder{src: src, reader: bufio.NewReader(src), start: time.Now(), onDone: onDone}
}

func (r *Recorder) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.scan(p[:n])
	}
	if err == io.EOF {
		r.finish()
	}
	return n, err
}

// scan accumulates bytes and emits a chunk per complete `data:` line.
func (r *Recorder) scan(b []byte) {
	r.buf.Write(b)
	s := r.buf.String()
	for {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(s[:idx])
		s = s[idx+1:]
		if data, ok := extractData(line); ok {
			r.chunks = append(r.chunks, store.Chunk{
				OffsetMs: time.Since(r.start).Milliseconds(),
				Data:     data,
			})
		}
	}
	r.buf.Reset()
	r.buf.WriteString(s)
}

func (r *Recorder) finish() {
	r.once.Do(func() {
		if r.onDone != nil {
			r.onDone(r.chunks)
		}
	})
}

func (r *Recorder) Close() error {
	r.finish()
	return r.src.Close()
}

// extractData pulls the payload from an SSE `data:` line or a raw NDJSON object.
func extractData(line string) (string, bool) {
	if strings.HasPrefix(line, "data:") {
		return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
	}
	if strings.HasPrefix(line, "{") {
		return line, true
	}
	return "", false
}

// Replay writes recorded chunks to w as an SSE stream, honoring the timing mode.
// accelFactor is used only when mode == Accelerated.
func Replay(w io.Writer, flush func(), chunks []store.Chunk, mode TimingMode, accelFactor float64) {
	var prev int64
	for _, c := range chunks {
		switch mode {
		case Realtime:
			if d := time.Duration(c.OffsetMs-prev) * time.Millisecond; d > 0 {
				time.Sleep(d)
			}
		case Accelerated:
			if accelFactor <= 0 {
				accelFactor = 10
			}
			if d := time.Duration(float64(c.OffsetMs-prev)/accelFactor) * time.Millisecond; d > 0 {
				time.Sleep(d)
			}
		case Instant:
			// no delay
		}
		prev = c.OffsetMs
		_, _ = io.WriteString(w, "data: "+c.Data+"\n\n")
		if flush != nil {
			flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flush != nil {
		flush()
	}
}
