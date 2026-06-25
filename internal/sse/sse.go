// Package sse records and replays Server-Sent Events / NDJSON streams with
// per-chunk timing fidelity.
//
// Recording is provider-faithful: it preserves the SSE `event:` name alongside
// the `data:` payload (Anthropic dispatches on event names), and on replay it
// reproduces the exact framing — including the provider's own terminator frame
// (`data: [DONE]` for OpenAI, `event: message_stop` for Anthropic) — rather than
// synthesizing an OpenAI-shaped `[DONE]`.
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
// unchanged while capturing each event (SSE frame or NDJSON line) with its
// offset from the first byte.
type Recorder struct {
	src    io.ReadCloser
	reader *bufio.Reader
	start  time.Time
	chunks []store.Chunk
	once   sync.Once
	onDone func([]store.Chunk)
	buf    strings.Builder

	// In-progress SSE frame (flushed on a blank line or EOF).
	curEvent string
	curData  []string
	inFrame  bool
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

// scan processes whole lines, buffering any trailing partial line.
func (r *Recorder) scan(b []byte) {
	r.buf.Write(b)
	s := r.buf.String()
	for {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(s[:idx], "\r")
		s = s[idx+1:]
		r.processLine(line)
	}
	r.buf.Reset()
	r.buf.WriteString(s)
}

// processLine handles one SSE field line, NDJSON object line, or blank delimiter.
func (r *Recorder) processLine(line string) {
	if line == "" { // blank line: dispatch the accumulated SSE frame
		r.dispatchFrame()
		return
	}
	if strings.HasPrefix(line, ":") { // SSE comment / keep-alive
		return
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// NDJSON: each line is a complete event with no SSE framing.
		r.emit("", trimmed)
		return
	}

	field, value := splitField(line)
	switch field {
	case "data":
		r.curData = append(r.curData, value)
		r.inFrame = true
	case "event":
		r.curEvent = value
		r.inFrame = true
	case "id", "retry":
		r.inFrame = true // part of the frame; value not needed for replay
	default:
		// Unknown field: ignore, per the SSE spec.
	}
}

func (r *Recorder) dispatchFrame() {
	if !r.inFrame {
		return
	}
	if len(r.curData) > 0 {
		r.emit(r.curEvent, strings.Join(r.curData, "\n"))
	}
	r.curEvent = ""
	r.curData = nil
	r.inFrame = false
}

func (r *Recorder) emit(event, data string) {
	r.chunks = append(r.chunks, store.Chunk{
		OffsetMs: time.Since(r.start).Milliseconds(),
		Event:    event,
		Data:     data,
	})
}

func (r *Recorder) finish() {
	r.once.Do(func() {
		if rem := strings.TrimRight(r.buf.String(), "\r\n"); rem != "" {
			r.processLine(rem)
		}
		r.dispatchFrame()
		if r.onDone != nil {
			r.onDone(r.chunks)
		}
	})
}

func (r *Recorder) Close() error {
	r.finish()
	return r.src.Close()
}

// splitField parses an SSE "field: value" line, stripping a single optional
// space after the colon (per the SSE spec).
func splitField(line string) (field, value string) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line, ""
	}
	field = line[:idx]
	value = line[idx+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value
}

// Replay writes recorded chunks to w, honoring the timing mode. When ndjson is
// true each chunk is emitted as a bare JSON line; otherwise SSE framing is used
// (an `event:` line when present, followed by `data:` line(s)). No synthetic
// terminator is appended — the provider's own terminator was recorded.
// accelFactor is used only when mode == Accelerated.
func Replay(w io.Writer, flush func(), chunks []store.Chunk, mode TimingMode, accelFactor float64, ndjson bool) {
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

		if ndjson {
			_, _ = io.WriteString(w, c.Data+"\n")
		} else {
			if c.Event != "" {
				_, _ = io.WriteString(w, "event: "+c.Event+"\n")
			}
			for _, dl := range strings.Split(c.Data, "\n") {
				_, _ = io.WriteString(w, "data: "+dl+"\n")
			}
			_, _ = io.WriteString(w, "\n")
		}
		if flush != nil {
			flush()
		}
	}
}
