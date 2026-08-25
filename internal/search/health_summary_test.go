package search

import (
	"context"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestPostgresStoreAggregatesHealthStatsSinceCutoff(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	now := time.Now().Truncate(time.Hour)
	// 同一批里不能出现两条 (site_key, bucket) 相同的记录：ON CONFLICT DO UPDATE
	// 不允许在一条语句里更新同一行两次。分两批写入，等价于真实的分次上报。
	err := store.AddHealthStats(context.Background(), []HealthStat{
		{SiteKey: "source", Bucket: now.Add(-25 * time.Hour), ErrorCount: 9, TotalMs: 900},
		{SiteKey: "source", Bucket: now, OKCount: 2, TotalMs: 200},
	})
	if err == nil {
		err = store.AddHealthStats(context.Background(), []HealthStat{
			{SiteKey: "source", Bucket: now, EmptyCount: 1, TotalMs: 100},
		})
	}
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
