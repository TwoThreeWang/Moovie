package search

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreAggregatesHealthStatsSinceCutoff(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().Truncate(time.Hour)
	err := store.AddHealthStats(context.Background(), []HealthStat{
		{SiteKey: "source", Bucket: now.Add(-25 * time.Hour), ErrorCount: 9, TotalMs: 900},
		{SiteKey: "source", Bucket: now, OKCount: 2, TotalMs: 200},
		{SiteKey: "source", Bucket: now, EmptyCount: 1, TotalMs: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := store.SummaryHealthSince(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries["source"]
	if summary == nil || summary.Total() != 3 || summary.OKRate() != 200.0/3.0 || summary.EmptyRate() != 100.0/3.0 || summary.AvgMs() != 100 || summary.Level() != "warn" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestHealthSummaryLevelsAndCircuitDeadline(t *testing.T) {
	if level := (&HealthSummary{OKCount: 1, EmptyCount: 10}).Level(); level != "bad" {
		t.Fatalf("empty-heavy level = %q", level)
	}
	health := NewHealth(true)
	now := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	health.now = func() time.Time { return now }
	for range breakerFailureThreshold {
		health.Record("source", OutcomeError, 10)
	}
	if got := health.TrippedUntil("source"); !got.Equal(now.Add(breakerCooldown)) {
		t.Fatalf("TrippedUntil() = %s", got)
	}
	now = now.Add(breakerCooldown + time.Second)
	if got := health.TrippedUntil("source"); !got.IsZero() {
		t.Fatalf("expired TrippedUntil() = %s", got)
	}
}
