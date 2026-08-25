package operations

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/feedback"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

func TestHealthAlertsPreserveThresholdPrecedenceAndCooldown(t *testing.T) {
	store := search.NewPostgresStore(testdb.Pool(t))
	for _, site := range []search.Site{{Key: "empty", BaseURL: "", Enabled: true}, {Key: "down", BaseURL: "", Enabled: true}, {Key: "small", BaseURL: "", Enabled: true}} {
		_, _ = store.CreateSite(t.Context(), site)
	}
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	_ = store.AddHealthStats(t.Context(), []search.HealthStat{
		{SiteKey: "empty", Bucket: now, EmptyCount: 99, ErrorCount: 1, TotalMs: 1000},
		{SiteKey: "down", Bucket: now, EmptyCount: 98, ErrorCount: 2, TotalMs: 500},
		{SiteKey: "small", Bucket: now, ErrorCount: 4, TotalMs: 40},
	})
	feedbackStore := feedback.NewPostgresStore(testdb.Pool(t))
	logs := captureLogs(t)
	service := NewService(store)
	service.now = func() time.Time { return now }
	_ = service.HandleHealthCheck(context.Background(), workqueue.Job{})
	_ = service.HandleHealthCheck(context.Background(), workqueue.Job{})
	if alerts := strings.Count(logs.String(), "site health alert"); alerts != 2 {
		t.Fatalf("alerts = %d, logs = %s", alerts, logs.String())
	}
	if !strings.Contains(logs.String(), `site_key=empty`) || !strings.Contains(logs.String(), "空返回率高达 99%") ||
		!strings.Contains(logs.String(), `site_key=down`) || !strings.Contains(logs.String(), "成功率为 0%") ||
		strings.Contains(logs.String(), `site_key=small`) {
		t.Fatalf("alert logs = %s", logs.String())
	}
	now = now.Add(siteAlertCooldown + time.Second)
	_ = store.AddHealthStats(t.Context(), []search.HealthStat{
		{SiteKey: "empty", Bucket: now, EmptyCount: 99, ErrorCount: 1, TotalMs: 1000},
		{SiteKey: "down", Bucket: now, EmptyCount: 98, ErrorCount: 2, TotalMs: 500},
	})
	_ = service.HandleHealthCheck(context.Background(), workqueue.Job{})
	if alerts := strings.Count(logs.String(), "site health alert"); alerts != 4 {
		t.Fatalf("alerts after cooldown = %d", alerts)
	}
	if count, _ := feedbackStore.CountPending(t.Context()); count != 0 {
		t.Fatalf("health check must not write feedback, got %d", count)
	}
}

func TestCleanupUsesRetentionWindows(t *testing.T) {
	store := &recordingStore{}
	var completedBefore, failedBefore time.Time
	var cleanupLimit int
	var telemetryBefore time.Time
	service := NewService(store, WithJobQueueCleanup(func(_ context.Context, completed, failed time.Time, limit int) (int, error) {
		completedBefore, failedBefore, cleanupLimit = completed, failed, limit
		return 0, nil
	}), WithTelemetryCleanup(func(_ context.Context, before time.Time, _ int) (int, error) {
		telemetryBefore = before
		return 0, nil
	}))
	service.now = func() time.Time { return time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC) }
	_ = service.HandleCleanup(context.Background(), workqueue.Job{})
	if store.inactiveDays != 10 || store.logDays != 30 || !store.healthBefore.Equal(service.now().AddDate(0, 0, -7)) {
		t.Fatalf("cleanup windows = %+v", store)
	}
	if !completedBefore.Equal(service.now().AddDate(0, 0, -3)) || !failedBefore.Equal(service.now().AddDate(0, 0, -3)) || cleanupLimit != 1000 {
		t.Fatalf("job cleanup = %s / %s / %d", completedBefore, failedBefore, cleanupLimit)
	}
	// 遥测保留必须明显长于最长读取窗口（activity_popular 的 7 天），
	// 否则首页活跃榜和候选质量分会因为清理而缺数据。
	if !telemetryBefore.Equal(service.now().AddDate(0, 0, -30)) {
		t.Fatalf("telemetry cleanup before = %s", telemetryBefore)
	}
}

type recordingStore struct {
	inactiveDays int
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
func (*recordingStore) PurgeStaleResources(context.Context, int) (int, error) { return 0, nil }
func (store *recordingStore) DeleteOldSearchLogs(_ context.Context, days int) (int, error) {
	store.logDays = days
	return 0, nil
}
func (store *recordingStore) DeleteHealthBefore(_ context.Context, before time.Time) (int, error) {
	store.healthBefore = before
	return 0, nil
}
