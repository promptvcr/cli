package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNilRulesNoOp(t *testing.T) {
	var r *Rules
	in := []byte(`{"a":1}`)
	if got := string(r.Body(in)); got != string(in) {
		t.Errorf("nil Body changed input: %q", got)
	}
	if got := r.Text("hello"); got != "hello" {
		t.Errorf("nil Text changed input: %q", got)
	}
}

func TestCompileEmptyIsNil(t *testing.T) {
	if Compile(nil, nil, "") != nil {
		t.Error("Compile with no rules should return nil")
	}
}

func TestBodyJSONPathWildcard(t *testing.T) {
	r := Compile([]string{"messages[*].content"}, nil, "")
	in := []byte(`{"model":"m","messages":[{"role":"user","content":"secret one"},{"role":"assistant","content":"secret two"}]}`)
	out := r.Body(in)

	var v struct {
		Model    string `json:"model"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("output not valid JSON: %v (%s)", err, out)
	}
	if v.Model != "m" {
		t.Errorf("model should be untouched, got %q", v.Model)
	}
	for i, m := range v.Messages {
		if m.Content != "REDACTED" {
			t.Errorf("messages[%d].content = %q, want REDACTED", i, m.Content)
		}
	}
}

func TestBodyJSONPathIndexAndCustomReplacement(t *testing.T) {
	r := Compile([]string{"items[1]"}, nil, "X")
	in := []byte(`{"items":["a","b","c"]}`)
	out := r.Body(in)
	var v struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v.Items[0] != "a" || v.Items[1] != "X" || v.Items[2] != "c" {
		t.Errorf("index masking wrong: %+v", v.Items)
	}
}

func TestBodyRegex(t *testing.T) {
	r := Compile(nil, []string{`sk-[A-Za-z0-9]{10,}`}, "REDACTED")
	in := []byte(`{"key":"sk-abcdef0123456789","note":"ok"}`)
	out := string(r.Body(in))
	if strings.Contains(out, "sk-abcdef") {
		t.Errorf("api key not redacted: %s", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("expected REDACTED marker: %s", out)
	}
}

func TestTextRegex(t *testing.T) {
	r := Compile(nil, []string{`\d{3}-\d{2}-\d{4}`}, "REDACTED")
	if got := r.Text("ssn 123-45-6789 end"); got != "ssn REDACTED end" {
		t.Errorf("Text regex = %q", got)
	}
}

func TestBodyNonJSONFallsBackToRegex(t *testing.T) {
	r := Compile([]string{"a.b"}, []string{"token=\\w+"}, "REDACTED")
	in := []byte("not json token=abc123")
	out := string(r.Body(in))
	if !strings.Contains(out, "REDACTED") || strings.Contains(out, "abc123") {
		t.Errorf("regex fallback failed: %s", out)
	}
}
