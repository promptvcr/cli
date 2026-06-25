// Package stats accumulates cache hit/miss/record counters and estimated dollar
// savings, both for a single proxy session (Snapshot) and cumulatively across
// sessions (Cumulative, persisted at ~/.promptvcr/stats.json).
//
// The call counts are exact. The dollar figure is an estimate (see the pricing
// package): it multiplies replayed calls by a per-model average cost.
package stats

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ProviderStat holds per-provider counters.
type ProviderStat struct {
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	Records int64   `json:"records"`
	Saved   float64 `json:"savedCents"`
}

// Snapshot is one proxy session's totals.
type Snapshot struct {
	Hits       int64
	Misses     int64
	Records    int64
	Saved      float64
	ByProvider map[string]ProviderStat
}

// Cumulative is the persisted lifetime total across sessions.
type Cumulative struct {
	Hits       int64                   `json:"hits"`
	Misses     int64                   `json:"misses"`
	Records    int64                   `json:"records"`
	Saved      float64                 `json:"savedCents"`
	Sessions   int64                   `json:"sessions"`
	ByProvider map[string]ProviderStat `json:"byProvider"`
	UpdatedAt  string                  `json:"updatedAt"`
}

const fileName = "stats.json"

// LoadCumulative reads dir/stats.json. A missing or malformed file yields an
// empty (zero) Cumulative; it never returns an error for those cases.
func LoadCumulative(dir string) *Cumulative {
	c := &Cumulative{ByProvider: map[string]ProviderStat{}}
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return c
	}
	if json.Unmarshal(b, c) != nil || c.ByProvider == nil {
		if c.ByProvider == nil {
			c.ByProvider = map[string]ProviderStat{}
		}
	}
	return c
}

// Add merges a session snapshot into the cumulative totals and counts a session.
func (c *Cumulative) Add(s Snapshot) {
	c.Hits += s.Hits
	c.Misses += s.Misses
	c.Records += s.Records
	c.Saved += s.Saved
	c.Sessions++
	if c.ByProvider == nil {
		c.ByProvider = map[string]ProviderStat{}
	}
	for prov, ps := range s.ByProvider {
		cur := c.ByProvider[prov]
		cur.Hits += ps.Hits
		cur.Misses += ps.Misses
		cur.Records += ps.Records
		cur.Saved += ps.Saved
		c.ByProvider[prov] = cur
	}
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// Save writes the cumulative totals atomically to dir/stats.json.
func (c *Cumulative) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, fileName)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ResetCumulative deletes the persisted stats file.
func ResetCumulative(dir string) error {
	err := os.Remove(filepath.Join(dir, fileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Dollars formats a cents value for display. Amounts of one cent or more render
// as dollars ($X.XX); positive sub-cent amounts render as a fractional cent
// (e.g. "0.10¢") so small-but-real savings don't collapse to a misleading
// "$0.00"; zero (or negative) renders as "$0.00".
func Dollars(cents float64) string {
	switch {
	case cents <= 0:
		return "$0.00"
	case cents < 1:
		return fmt.Sprintf("%.2f¢", cents)
	default:
		return fmt.Sprintf("$%.2f", cents/100)
	}
}

// View is a renderable summary, used for both a session and cumulative totals,
// optionally augmented with store-inventory numbers.
type View struct {
	Title      string
	Hits       int64
	Misses     int64
	Records    int64
	Saved      float64
	Sessions   int64 // 0 hides the line
	ByProvider map[string]ProviderStat
	Cassettes  int // <0 hides the inventory lines
	Stale      int
}

// View converts a session snapshot into a renderable View.
func (s Snapshot) View(title string) View {
	return View{
		Title: title, Hits: s.Hits, Misses: s.Misses, Records: s.Records,
		Saved: s.Saved, ByProvider: s.ByProvider, Cassettes: -1,
	}
}

// View converts cumulative totals into a renderable View.
func (c *Cumulative) View(title string) View {
	return View{
		Title: title, Hits: c.Hits, Misses: c.Misses, Records: c.Records,
		Saved: c.Saved, Sessions: c.Sessions, ByProvider: c.ByProvider, Cassettes: -1,
	}
}

func (v View) hitRate() float64 {
	total := v.Hits + v.Misses
	if total == 0 {
		return 0
	}
	return 100 * float64(v.Hits) / float64(total)
}

func (v View) providerNames() []string {
	names := make([]string, 0, len(v.ByProvider))
	for k := range v.ByProvider {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// WriteText renders a plain-text summary (used on stderr and for the CLI).
func (v View) WriteText(w io.Writer) {
	fmt.Fprintf(w, "%s\n", v.Title)
	fmt.Fprintf(w, "  replays (hits):    %d\n", v.Hits)
	fmt.Fprintf(w, "  misses:            %d\n", v.Misses)
	fmt.Fprintf(w, "  recorded:          %d\n", v.Records)
	fmt.Fprintf(w, "  hit rate:          %.0f%%\n", v.hitRate())
	fmt.Fprintf(w, "  estimated saved:   %s (estimate)\n", Dollars(v.Saved))
	if v.Sessions > 0 {
		fmt.Fprintf(w, "  sessions:          %d\n", v.Sessions)
	}
	if v.Cassettes >= 0 {
		fmt.Fprintf(w, "  cassettes on disk: %d", v.Cassettes)
		if v.Stale > 0 {
			fmt.Fprintf(w, " (%d stale)", v.Stale)
		}
		fmt.Fprintln(w)
	}
	for _, p := range v.providerNames() {
		ps := v.ByProvider[p]
		fmt.Fprintf(w, "    %-11s %d hit / %d miss / %d rec  %s\n",
			p, ps.Hits, ps.Misses, ps.Records, Dollars(ps.Saved))
	}
}

// WriteMarkdown renders a GitHub-flavored markdown summary for $GITHUB_STEP_SUMMARY.
func (v View) WriteMarkdown(w io.Writer) {
	fmt.Fprintf(w, "## %s\n\n", v.Title)
	fmt.Fprintf(w, "| Metric | Value |\n| --- | --- |\n")
	fmt.Fprintf(w, "| Replays (cache hits) | %d |\n", v.Hits)
	fmt.Fprintf(w, "| Misses | %d |\n", v.Misses)
	fmt.Fprintf(w, "| Recorded | %d |\n", v.Records)
	fmt.Fprintf(w, "| Hit rate | %.0f%% |\n", v.hitRate())
	fmt.Fprintf(w, "| Estimated saved | %s (estimate) |\n", Dollars(v.Saved))
	if v.Sessions > 0 {
		fmt.Fprintf(w, "| Sessions | %d |\n", v.Sessions)
	}
	if v.Cassettes >= 0 {
		stale := ""
		if v.Stale > 0 {
			stale = fmt.Sprintf(" (%d stale)", v.Stale)
		}
		fmt.Fprintf(w, "| Cassettes on disk | %d%s |\n", v.Cassettes, stale)
	}
	if len(v.ByProvider) > 0 {
		fmt.Fprintf(w, "\n| Provider | Hits | Misses | Recorded | Saved |\n| --- | --- | --- | --- | --- |\n")
		for _, p := range v.providerNames() {
			ps := v.ByProvider[p]
			fmt.Fprintf(w, "| %s | %d | %d | %d | %s |\n", p, ps.Hits, ps.Misses, ps.Records, Dollars(ps.Saved))
		}
	}
	fmt.Fprintln(w)
}
