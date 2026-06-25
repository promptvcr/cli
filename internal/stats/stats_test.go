package stats

import (
	"strings"
	"testing"
)

func sampleSnapshot() Snapshot {
	return Snapshot{
		Hits: 8, Misses: 2, Records: 2, Saved: 20,
		ByProvider: map[string]ProviderStat{
			"openai":    {Hits: 5, Misses: 1, Records: 1, Saved: 12},
			"anthropic": {Hits: 3, Misses: 1, Records: 1, Saved: 8},
		},
	}
}

func TestCumulativeAddAndMerge(t *testing.T) {
	c := &Cumulative{ByProvider: map[string]ProviderStat{}}
	c.Add(sampleSnapshot())
	c.Add(sampleSnapshot())

	if c.Hits != 16 || c.Misses != 4 || c.Records != 4 {
		t.Errorf("totals wrong: %+v", c)
	}
	if c.Saved != 40.0 {
		t.Errorf("saved = %v, want 40", c.Saved)
	}
	if c.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", c.Sessions)
	}
	if c.ByProvider["openai"].Hits != 10 || c.ByProvider["anthropic"].Saved != 16 {
		t.Errorf("per-provider merge wrong: %+v", c.ByProvider)
	}
	if c.UpdatedAt == "" {
		t.Error("UpdatedAt should be set")
	}
}

func TestSaveLoadResetRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Loading a missing file yields zeros, not an error.
	empty := LoadCumulative(dir)
	if empty.Hits != 0 || empty.ByProvider == nil {
		t.Errorf("fresh load wrong: %+v", empty)
	}

	c := &Cumulative{ByProvider: map[string]ProviderStat{}}
	c.Add(sampleSnapshot())
	if err := c.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := LoadCumulative(dir)
	if got.Hits != 8 || got.Saved != 20 || got.ByProvider["openai"].Hits != 5 {
		t.Errorf("round trip mismatch: %+v", got)
	}

	if err := ResetCumulative(dir); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if after := LoadCumulative(dir); after.Hits != 0 {
		t.Errorf("reset did not clear: %+v", after)
	}
	// Resetting an already-missing file is a no-op, not an error.
	if err := ResetCumulative(dir); err != nil {
		t.Errorf("reset on missing file errored: %v", err)
	}
}

func TestDollars(t *testing.T) {
	if got := Dollars(1250); got != "$12.50" {
		t.Errorf("Dollars(1250) = %q", got)
	}
	if got := Dollars(0); got != "$0.00" {
		t.Errorf("Dollars(0) = %q", got)
	}
}

func TestRenderText(t *testing.T) {
	var b strings.Builder
	sampleSnapshot().View("Session").WriteText(&b)
	out := b.String()
	for _, want := range []string{"Session", "replays (hits):    8", "estimated saved:   $0.20", "openai", "anthropic"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	var b strings.Builder
	v := sampleSnapshot().View("Savings")
	v.WriteMarkdown(&b)
	out := b.String()
	if !strings.Contains(out, "## Savings") || !strings.Contains(out, "| Replays (cache hits) | 8 |") {
		t.Errorf("markdown output wrong:\n%s", out)
	}
	if !strings.Contains(out, "| Provider | Hits |") {
		t.Errorf("markdown missing provider table:\n%s", out)
	}
}
