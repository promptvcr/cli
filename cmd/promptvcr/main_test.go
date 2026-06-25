package main

import (
	"testing"
	"time"

	"github.com/promptvcr/cli/internal/store"
)

func rfc3339Ago(d time.Duration) string {
	return time.Now().Add(-d).UTC().Format(time.RFC3339)
}

func TestCountStale(t *testing.T) {
	recs := []*store.Record{
		{RecordedAt: rfc3339Ago(2 * 24 * time.Hour)},   // fresh
		{RecordedAt: rfc3339Ago(40 * 24 * time.Hour)},  // stale at 30d
		{RecordedAt: rfc3339Ago(100 * 24 * time.Hour)}, // stale
		{RecordedAt: "not-a-date"},                     // ignored
	}
	if got := countStale(recs, 30); got != 2 {
		t.Errorf("countStale(30) = %d, want 2", got)
	}
	if got := countStale(recs, 200); got != 0 {
		t.Errorf("countStale(200) = %d, want 0", got)
	}
	// staleDays <= 0 falls back to the default threshold.
	if got := countStale(recs, 0); got != 2 {
		t.Errorf("countStale(0) should use default, got %d", got)
	}
}

func TestAgeAndTag(t *testing.T) {
	cutoff := time.Now().AddDate(0, 0, -30)

	if age, tag := ageAndTag(rfc3339Ago(40*24*time.Hour), cutoff); tag != "STALE" {
		t.Errorf("40d-old should be STALE, got tag=%q age=%q", tag, age)
	}
	if _, tag := ageAndTag(rfc3339Ago(2*24*time.Hour), cutoff); tag != "" {
		t.Errorf("2d-old should not be STALE, got %q", tag)
	}
	if age, tag := ageAndTag("garbage", cutoff); age != "?" || tag != "" {
		t.Errorf("unparseable date = (%q,%q), want (?,'')", age, tag)
	}
}

func TestHumanizeAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{3 * 24 * time.Hour, "3d"},
		{5 * time.Hour, "5h"},
		{10 * time.Minute, "10m"},
		{10 * time.Second, "new"},
	}
	for _, tc := range cases {
		if got := humanizeAge(tc.d); got != tc.want {
			t.Errorf("humanizeAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
