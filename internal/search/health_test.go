package search

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHealthUsesFailureThresholdProbeAndCooldown(t *testing.T) {
	now := time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC)
	health := NewHealth(true)
	health.now = func() time.Time { return now }
	for range breakerFailureThreshold {
		health.Record("broken", OutcomeError, 1)
	}
	sites := []Site{{Key: "broken"}, {Key: "healthy"}}
	available, skipped := health.FilterAvailable(sites)
	if len(available) != 2 || len(skipped) != 0 {
		t.Fatalf("first open-circuit call must include one probe: available=%v skipped=%v", available, skipped)
	}
	available, skipped = health.FilterAvailable(sites)
	if len(available) != 1 || available[0].Key != "healthy" || len(skipped) != 1 || skipped[0] != "broken" {
		t.Fatalf("second call should skip broken site: available=%v skipped=%v", available, skipped)
	}
	now = now.Add(breakerCooldown + time.Second)
	available, skipped = health.FilterAvailable(sites)
	if len(available) != 2 || len(skipped) != 0 {
		t.Fatalf("site was not restored after cooldown: available=%v skipped=%v", available, skipped)
	}
}

func TestHealthFlushesHourlyCountersOnStop(t *testing.T) {
	store := &recordingHealthStatStore{}
	health := NewHealthWithStore(true, store)
	health.now = func() time.Time { return time.Date(2026, time.July, 29, 12, 45, 0, 0, time.UTC) }
	health.Record("site", OutcomeOK, 120)
	health.Record("site", OutcomeEmpty, 30)
	health.Record("site", OutcomeTimeout, 500)
	health.Record("site", OutcomeError, 50)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := health.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(store.stats) != 1 {
		t.Fatalf("stats = %+v", store.stats)
	}
	stat := store.stats[0]
	if stat.OKCount != 1 || stat.EmptyCount != 1 || stat.TimeoutCount != 1 || stat.ErrorCount != 1 || stat.TotalMs != 700 {
		t.Fatalf("stat counts = %+v", stat)
	}
	if !stat.Bucket.Equal(time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket = %s", stat.Bucket)
	}
}

type recordingHealthStatStore struct {
	mu    sync.Mutex
	stats []HealthStat
}

func (store *recordingHealthStatStore) AddHealthStats(_ context.Context, stats []HealthStat) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.stats = append(store.stats, stats...)
	return nil
}

func TestHealthNeverFiltersEverySite(t *testing.T) {
	health := NewHealth(true)
	for range breakerFailureThreshold {
		health.Record("only", OutcomeTimeout, 1)
	}
	sites := []Site{{Key: "only"}}
	_, _ = health.FilterAvailable(sites) // consumes the half-open probe
	available, skipped := health.FilterAvailable(sites)
	if len(available) != 1 || len(skipped) != 0 {
		t.Fatalf("all-tripped fallback changed: available=%v skipped=%v", available, skipped)
	}
}
