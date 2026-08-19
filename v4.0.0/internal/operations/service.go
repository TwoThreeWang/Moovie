package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

const (
	siteStatRetentionDays     = 7
	completedJobRetentionDays = 30
	failedJobRetentionDays    = 90
	jobCleanupBatchSize       = 1000
	// 遥测读取方最长只看 7 天，留 30 天是给排查留余量，不是给查询用的。
	telemetryRetentionDays = 30
	// 每天每张表最多删 100 万行：够排空存量积压，又不会让一次清理跑太久。
	telemetryCleanupBudget = 1_000_000
	siteAlertMinSamples    = 5
	siteAlertCooldown      = 24 * time.Hour
	TaskCleanup            = "operations_cleanup"
	TaskHealthCheck        = "site_health_check"
)

type Store interface {
	ListEnabled(ctx context.Context) ([]search.Site, error)
	SummaryHealthSince(ctx context.Context, since time.Time) (map[string]*search.HealthSummary, error)
	DeleteInactive(ctx context.Context, days int) (int, error)
	DeleteOldKeywords(ctx context.Context, days int) (int, error)
	DeleteOldSearchLogs(ctx context.Context, days int) (int, error)
	DeleteHealthBefore(ctx context.Context, before time.Time) (int, error)
}

type Service struct {
	store Store
	now   func() time.Time

	mu               sync.Mutex
	lastAlert        map[string]time.Time
	jobCleanup       func(context.Context, time.Time, time.Time, int) (int, error)
	telemetryCleanup func(context.Context, time.Time, int) (int, error)
}

type ServiceOption func(*Service)

func WithJobQueueCleanup(cleanup func(context.Context, time.Time, time.Time, int) (int, error)) ServiceOption {
	return func(service *Service) { service.jobCleanup = cleanup }
}

func WithTelemetryCleanup(cleanup func(context.Context, time.Time, int) (int, error)) ServiceOption {
	return func(service *Service) { service.telemetryCleanup = cleanup }
}

func NewService(store Store, options ...ServiceOption) *Service {
	service := &Service{
		store: store, now: time.Now, lastAlert: make(map[string]time.Time),
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *Service) HandleCleanup(ctx context.Context, _ workqueue.Job) error {
	type cleanupOperation struct {
		name string
		run  func() (int, error)
	}
	operations := []cleanupOperation{
		{name: "inactive VOD items", run: func() (int, error) { return service.store.DeleteInactive(ctx, 10) }},
		{name: "old trending keywords", run: func() (int, error) { return service.store.DeleteOldKeywords(ctx, 30) }},
		{name: "old search logs", run: func() (int, error) { return service.store.DeleteOldSearchLogs(ctx, 30) }},
		{name: "old site health stats", run: func() (int, error) {
			return service.store.DeleteHealthBefore(ctx, service.now().AddDate(0, 0, -siteStatRetentionDays))
		}},
	}
	if service.jobCleanup != nil {
		now := service.now()
		operations = append(operations, cleanupOperation{name: "expired worker jobs", run: func() (int, error) {
			return service.jobCleanup(ctx, now.AddDate(0, 0, -completedJobRetentionDays), now.AddDate(0, 0, -failedJobRetentionDays), jobCleanupBatchSize)
		}})
	}
	if service.telemetryCleanup != nil {
		before := service.now().AddDate(0, 0, -telemetryRetentionDays)
		operations = append(operations, cleanupOperation{name: "expired playback telemetry", run: func() (int, error) {
			return service.telemetryCleanup(ctx, before, telemetryCleanupBudget)
		}})
	}
	var failures []error
	for _, operation := range operations {
		affected, err := operation.run()
		if err != nil {
			slog.Error("scheduled cleanup failed", "operation", operation.name, "error", err)
			failures = append(failures, err)
			continue
		}
		slog.Info("scheduled cleanup completed", "operation", operation.name, "affected", affected)
	}
	return errors.Join(failures...)
}

func (service *Service) HandleHealthCheck(ctx context.Context, _ workqueue.Job) error {
	sites, err := service.store.ListEnabled(ctx)
	if err != nil || len(sites) == 0 {
		return err
	}
	summaries, err := service.store.SummaryHealthSince(ctx, service.now().Add(-time.Hour))
	if err != nil {
		return err
	}
	for _, site := range sites {
		summary := summaries[site.Key]
		if summary == nil || summary.Total() < siteAlertMinSamples {
			continue
		}
		reason := ""
		switch {
		case summary.EmptyRate() > 98:
			reason = fmt.Sprintf("空返回率高达 %.0f%%，接口结构可能已变更", summary.EmptyRate())
		case summary.OKRate() == 0:
			reason = fmt.Sprintf("成功率为 0%%（超时 %d 次、错误 %d 次）", summary.TimeoutCount, summary.ErrorCount)
		default:
			continue
		}
		if !service.shouldAlert(site.Key) {
			continue
		}
		slog.Warn("site health alert",
			"site_key", site.Key,
			"reason", reason,
			"samples", summary.Total(),
			"avg_ms", summary.AvgMs(),
		)
	}
	return nil
}

func (service *Service) shouldAlert(siteKey string) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	now := service.now()
	if last, exists := service.lastAlert[siteKey]; exists && now.Sub(last) < siteAlertCooldown {
		return false
	}
	service.lastAlert[siteKey] = now
	return true
}
