package hash

import "testing"

// goldenKey is produced by the TypeScript mirror (packages/drift-core/src/hash.ts):
//
//	cacheKey({ method:"post", host:"api.openai.com", path:"/v1/chat/completions",
//	  query:"", body: JSON.stringify({model:"gpt-5.2",
//	  messages:[{role:"user",content:"hi <b>"}], temperature:0.7, meta:{z:1,a:2}}),
//	  ignorePaths:["temperature"] })
//
// If you change the hashing algorithm, recompute this in BOTH languages.
const goldenKey = "7e6370acabba400a2b0459be88e138f55e938edad227c207e90669ed3827ee02"

func TestKeyParityWithTypeScript(t *testing.T) {
	// Keys intentionally in a different order than the TS input to also exercise
	// canonicalization. The "<b>" exercises HTML-escaping parity.
	body := []byte(`{"meta":{"a":2,"z":1},"temperature":0.7,"model":"gpt-5.2","messages":[{"content":"hi <b>","role":"user"}]}`)
	got := Key(Input{
		Method:      "post",
		Host:        "api.openai.com",
		Path:        "/v1/chat/completions",
		Body:        body,
		IgnorePaths: []string{"temperature"},
	})
	if got != goldenKey {
		t.Fatalf("cross-language key mismatch:\n  got    %s\n  golden %s", got, goldenKey)
	}
}

// hardGolden is produced by the TypeScript mirror (drift-core hash.test.ts,
// "matches the Go mirror on hard canonicalization cases"). It exercises
// U+2028/U+2029 (Go escapes these; JS does not), non-ASCII, HTML chars, floats,
// and large integers. Recompute in BOTH languages if the algorithm changes.
const hardGolden = "72f4e7916268d66255c376ff55a730aea49c2605ef8257eabe4c27f2c6ca5771"

func TestKeyParityWithTypeScript_HardCases(t *testing.T) {
	// Note the literal \u2028 / \u2029 runes and embedded non-ASCII + HTML chars.
	body := []byte("{\"z\":1,\"a\":2,\"text\":\"a\u2028b\u2029c <tag> & \\\"q\\\" 中é\",\"temp\":0.7,\"max_tokens\":1024,\"big\":1000000000000,\"nested\":{\"y\":[3,2,1],\"x\":null}}")
	got := Key(Input{
		Method: "post",
		Host:   "api.openai.com",
		Path:   "/v1/chat/completions",
		Body:   body,
	})
	if got != hardGolden {
		t.Fatalf("cross-language key mismatch on hard cases:\n  got    %s\n  golden %s", got, hardGolden)
	}
}

func TestKeyIsOrderIndependent(t *testing.T) {
	a := Key(Input{Method: "POST", Host: "h", Path: "/p", Body: []byte(`{"a":1,"b":2}`)})
	b := Key(Input{Method: "POST", Host: "h", Path: "/p", Body: []byte(`{"b":2,"a":1}`)})
	if a != b {
		t.Fatalf("expected order-independent keys, got %s vs %s", a, b)
	}
}

func TestKeyIgnorePathsMatchesOmittedField(t *testing.T) {
	withIgnored := Key(Input{
		Method: "POST", Host: "h", Path: "/p",
		Body:        []byte(`{"model":"x","request_id":"abc"}`),
		IgnorePaths: []string{"request_id"},
	})
	omitted := Key(Input{
		Method: "POST", Host: "h", Path: "/p",
		Body: []byte(`{"model":"x"}`),
	})
	if withIgnored != omitted {
		t.Fatalf("ignoring a field should equal omitting it: %s vs %s", withIgnored, omitted)
	}
}

func TestKeyDiffersOnStructuralChange(t *testing.T) {
	num := Key(Input{Method: "POST", Host: "h", Path: "/p", Body: []byte(`{"total":42}`)})
	str := Key(Input{Method: "POST", Host: "h", Path: "/p", Body: []byte(`{"total":"42"}`)})
	if num == str {
		t.Fatal("number vs string body should produce different keys")
	}
}

func TestRedactStripsSecretsKeepsRest(t *testing.T) {
	in := map[string][]string{
		"Authorization":  {"Bearer sk-secret"},
		"X-Api-Key":      {"secret"},
		"X-Goog-Api-Key": {"secret"},
		"Content-Type":   {"application/json"},
	}
	out := Redact(in)
	for _, secret := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		if out[secret][0] != "REDACTED" {
			t.Errorf("%s should be redacted, got %q", secret, out[secret][0])
		}
	}
	if out["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type should be preserved, got %q", out["Content-Type"][0])
	}
}
