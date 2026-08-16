package operations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/feedback"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func TestHealthAlertsPreserveThresholdPrecedenceAndCooldown(t *testing.T) {
	store := search.NewMemoryStore()
	store.ReplaceSites([]search.Site{{Key: "empty", Enabled: true}, {Key: "down", Enabled: true}, {Key: "small", Enabled: true}})
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	_ = store.AddHealthStats(t.Context(), []search.HealthStat{
		{SiteKey: "empty", Bucket: now, EmptyCount: 99, ErrorCount: 1, TotalMs: 1000},
		{SiteKey: "down", Bucket: now, EmptyCount: 98, ErrorCount: 2, TotalMs: 500},
		{SiteKey: "small", Bucket: now, ErrorCount: 4, TotalMs: 40},
	})
	feedbackStore := feedback.NewMemoryStore()
	service := NewService(store, feedbackStore)
	service.now = func() time.Time { return now }
	_ = service.HandleHealthCheck(context.Background(), workqueue.Job{})
	_ = service.HandleHealthCheck(context.Background(), workqueue.Job{})
	records, _ := feedbackStore.ListAdmin(t.Context(), "", 10, 0)
	if len(records) != 2 {
		t.Fatalf("alerts = %+v", records)
	}
	for _, record := range records {
		if record.Type != feedback.TypeSystemAlert {
			t.Fatalf("alert type = %q", record.Type)
		}
	}
	contents := records[0].Content + "\n" + records[1].Content
	if !strings.Contains(contents, "empty 最近 1 小时异常：空返回率高达 99%") || !strings.Contains(contents, "down 最近 1 小时异常：成功率为 0%") || strings.Contains(contents, "small") {
		t.Fatalf("alert contents = %s", contents)
	}
	now = now.Add(siteAlertCooldown + time.Second)
	_ = store.AddHealthStats(t.Context(), []search.HealthStat{
		{SiteKey: "empty", Bucket: now, EmptyCount: 99, ErrorCount: 1, TotalMs: 1000},
		{SiteKey: "down", Bucket: now, EmptyCount: 98, ErrorCount: 2, TotalMs: 500},
	})
	_ = service.HandleHealthCheck(context.Background(), workqueue.Job{})
	if count, _ := feedbackStore.CountPending(t.Context()); count != 4 {
		t.Fatalf("alerts after cooldown = %d", count)
	}
}

func TestCleanupUsesRetentionWindows(t *testing.T) {
	store := &recordingStore{}
	var completedBefore, failedBefore time.Time
	var cleanupLimit int
	service := NewService(store, feedback.NewMemoryStore(), WithJobQueueCleanup(func(_ context.Context, completed, failed time.Time, limit int) (int, error) {
		completedBefore, failedBefore, cleanupLimit = completed, failed, limit
		return 0, nil
	}))
	service.now = func() time.Time { return time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC) }
	_ = service.HandleCleanup(context.Background(), workqueue.Job{})
	if store.inactiveDays != 10 || store.keywordDays != 30 || store.logDays != 30 || !store.healthBefore.Equal(service.now().AddDate(0, 0, -7)) {
		t.Fatalf("cleanup windows = %+v", store)
	}
	if !completedBefore.Equal(service.now().AddDate(0, 0, -30)) || !failedBefore.Equal(service.now().AddDate(0, 0, -90)) || cleanupLimit != 1000 {
		t.Fatalf("job cleanup = %s / %s / %d", completedBefore, failedBefore, cleanupLimit)
	}
}

type recordingStore struct {
	inactiveDays int
	keywordDays  int
	logDays      int
	healthBefore time.Time
}

func (*recordingStore) ListEnabled(context.Context) ([]search.Site, error) { return nil, nil }
func (*recordingStore) SummaryHealthSince(context.Context, time.Time) (map[string]*search.HealthSummary, error) {
	return nil, nil
}
func (store *recordingStore) DeleteInactive(_ context.Context, days int) (int, error) {
	store.inactiveDays = days
	return 0, nil
}
func (store *recordingStore) DeleteOldKeywords(_ context.Context, days int) (int, error) {
	store.keywordDays = days
	return 0, nil
}
func (store *recordingStore) DeleteOldSearchLogs(_ context.Context, days int) (int, error) {
	store.logDays = days
	return 0, nil
}
func (store *recordingStore) DeleteHealthBefore(_ context.Context, before time.Time) (int, error) {
	store.healthBefore = before
	return 0, nil
}
