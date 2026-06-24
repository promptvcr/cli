// Package hash derives deterministic, secret-free cache keys for requests.
//
// The key must be stable across runs and machines and must never depend on
// secrets. We strip volatile/secret fields, canonicalize the JSON body
// (recursively sorted keys), and SHA-256 the composed canonical string.
//
// Keep this in sync with packages/drift-core/src/hash.ts.
package hash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// RedactHeaders are header names (lowercased) that must never be persisted or
// influence the cache key.
var RedactHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"api-key":             true,
	"openai-organization": true,
	"openai-project":      true,
	"x-goog-api-key":      true,
	"x-request-id":        true,
	"user-agent":          true,
	"date":                true,
	"cookie":              true,
	"set-cookie":          true,
}

// canonical recursively canonicalizes a decoded JSON value. Objects are left as
// maps: encoding/json marshals map keys in sorted order, which (combined with
// HTML-escaping disabled in marshalCanonical) yields byte-identical output to
// the TypeScript mirror's `JSON.stringify(sortedObject)`.
func canonical(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = canonical(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = canonical(t[i])
		}
		return t
	default:
		return v
	}
}

// marshalCanonical serializes v the same way JavaScript's JSON.stringify does:
// sorted object keys (via map marshaling) and NO HTML escaping of <, >, &.
func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1] // Encode appends a trailing newline
	}
	return b, nil
}

// stripIgnored removes dot-path fields (e.g. "metadata.request_id") from a
// decoded JSON body before hashing.
func stripIgnored(v any, dotPath string) {
	parts := strings.Split(dotPath, ".")
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for i := 0; i < len(parts)-1; i++ {
		next, ok := m[parts[i]].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
	delete(m, parts[len(parts)-1])
}

// Input describes a request to be hashed.
type Input struct {
	Method      string
	Host        string
	Path        string
	Query       string
	Body        []byte
	IgnorePaths []string
}

// Key returns the deterministic SHA-256 cache key (hex) for a request.
func Key(in Input) string {
	canonicalBody := in.Body
	var parsed any
	if len(in.Body) > 0 && json.Unmarshal(in.Body, &parsed) == nil {
		for _, p := range in.IgnorePaths {
			stripIgnored(parsed, p)
		}
		if b, err := marshalCanonical(canonical(parsed)); err == nil {
			canonicalBody = b
		}
	}

	h := sha256.New()
	h.Write([]byte(strings.ToUpper(in.Method)))
	h.Write([]byte{'\n'})
	h.Write([]byte(in.Host))
	h.Write([]byte{'\n'})
	h.Write([]byte(in.Path))
	h.Write([]byte{'\n'})
	h.Write([]byte(in.Query))
	h.Write([]byte{'\n'})
	h.Write(canonicalBody)
	return hex.EncodeToString(h.Sum(nil))
}

// Redact returns a copy of headers with secret/volatile values replaced.
func Redact(headers map[string][]string) map[string][]string {
	out := make(map[string][]string, len(headers))
	for k, v := range headers {
		if RedactHeaders[strings.ToLower(k)] {
			out[k] = []string{"REDACTED"}
		} else {
			out[k] = v
		}
	}
	return out
}
