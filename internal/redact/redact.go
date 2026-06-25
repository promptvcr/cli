// Package redact masks sensitive values out of recorded request/response bodies
// and stream chunks before they are written to disk. It supports a small JSON
// path syntax (dot segments with [*] or [n] array selectors, e.g.
// "messages[*].content") and raw regular expressions applied to the serialized
// text.
//
// Redaction never affects the cache key: hashing happens before redaction, and
// auth headers are always stripped regardless of these rules.
package redact

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Rules is a compiled, immutable set of redaction rules. A nil *Rules is a
// valid no-op.
type Rules struct {
	paths    [][]seg
	patterns []*regexp.Regexp
	replace  string
}

type seg struct {
	key string
	arr bool
	idx int // -1 means wildcard [*]
}

// Compile builds Rules from raw JSON paths and regex patterns. replaceWith
// defaults to "REDACTED". Invalid regexes are skipped.
func Compile(jsonPaths, patterns []string, replaceWith string) *Rules {
	if len(jsonPaths) == 0 && len(patterns) == 0 {
		return nil
	}
	r := &Rules{replace: replaceWith}
	if r.replace == "" {
		r.replace = "REDACTED"
	}
	for _, p := range jsonPaths {
		if segs := parsePath(p); segs != nil {
			r.paths = append(r.paths, segs)
		}
	}
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			r.patterns = append(r.patterns, re)
		}
	}
	return r
}

func parsePath(path string) []seg {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var out []seg
	for _, tok := range strings.Split(path, ".") {
		if tok == "" {
			continue
		}
		i := strings.IndexByte(tok, '[')
		if i < 0 {
			out = append(out, seg{key: tok})
			continue
		}
		base := tok[:i]
		inner := strings.TrimSuffix(strings.TrimPrefix(tok[i:], "["), "]")
		s := seg{key: base, arr: true, idx: -1}
		if inner != "*" {
			if n, err := strconv.Atoi(inner); err == nil {
				s.idx = n
			}
		}
		out = append(out, s)
	}
	return out
}

// Body applies JSON-path masking (if the body parses as JSON) and then regex
// replacement to the serialized bytes. It returns the (possibly compacted)
// result; on any parse failure it falls back to regex-only over the input.
func (r *Rules) Body(b []byte) []byte {
	if r == nil || len(b) == 0 {
		return b
	}
	if len(r.paths) > 0 {
		var v any
		if json.Unmarshal(b, &v) == nil {
			for _, segs := range r.paths {
				maskPath(v, segs, r.replace)
			}
			if nb, err := json.Marshal(v); err == nil {
				b = nb
			}
		}
	}
	return r.applyPatterns(b)
}

// Text applies only regex replacement, for SSE/NDJSON chunk payloads.
func (r *Rules) Text(s string) string {
	if r == nil {
		return s
	}
	return string(r.applyPatterns([]byte(s)))
}

func (r *Rules) applyPatterns(b []byte) []byte {
	for _, re := range r.patterns {
		b = re.ReplaceAll(b, []byte(r.replace))
	}
	return b
}

// maskPath walks node following segs and replaces the matched leaf value(s)
// with replace. Maps and slice elements are mutated in place.
func maskPath(node any, segs []seg, replace string) {
	if len(segs) == 0 {
		return
	}
	s := segs[0]
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	child, exists := m[s.key]
	if !exists {
		return
	}
	if s.arr {
		arr, ok := child.([]any)
		if !ok {
			return
		}
		if s.idx == -1 {
			for i := range arr {
				if len(segs) == 1 {
					arr[i] = replace
				} else {
					maskPath(arr[i], segs[1:], replace)
				}
			}
			return
		}
		if s.idx >= 0 && s.idx < len(arr) {
			if len(segs) == 1 {
				arr[s.idx] = replace
			} else {
				maskPath(arr[s.idx], segs[1:], replace)
			}
		}
		return
	}
	if len(segs) == 1 {
		m[s.key] = replace
		return
	}
	maskPath(child, segs[1:], replace)
}
