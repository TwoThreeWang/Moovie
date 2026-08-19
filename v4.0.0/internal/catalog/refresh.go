package catalog

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

const (
	RefreshProviderDouban    = "douban_metadata"
	RefreshProviderReviews   = "douban_reviews"
	RefreshProviderTMDB      = "tmdb"
	RefreshProviderEmbedding = "embedding"

	// RefreshReasonPartialMetadata 是详情页发现资料不全时自己触发的入队。
	RefreshReasonPartialMetadata = "partial_metadata"
)

type RefreshQueue interface {
	EnqueueRefresh(ctx context.Context, doubanID, provider, reason string, requestedBy int) (int, error)
}

type MediaRefreshQueue interface {
	EnqueueMediaRefresh(ctx context.Context, mediaID int, reason string, requestedBy int) (int, error)
}

func (store *PostgresStore) EnqueueRefresh(ctx context.Context, doubanID, provider, reason string, requestedBy int) (int, error) {
	if !validDoubanID(doubanID) {
		return 0, workqueue.Terminal(fmt.Errorf("invalid Douban ID %q", doubanID))
	}
	if provider == "" {
		provider = RefreshProviderDouban
	}
	if !validRefreshProvider(provider) {
		return 0, workqueue.Terminal(fmt.Errorf("invalid metadata refresh provider %q", provider))
	}
	jobID, err := workqueue.NewPostgresStore(store.database).Enqueue(ctx, workqueue.Spec{
		TaskType: provider, SubjectKey: doubanID, Payload: map[string]string{"douban_id": doubanID},
		Reason: reason, RequestedBy: requestedBy,
	})
	if err == nil && reason == RefreshReasonPartialMetadata {
		// 详情页触发的补全入队必须自己推进 next_refresh_at。抓取失败时 updateRefreshState
		// 根本不会执行，next_refresh_at 就一直停在过去，于是这个页面每被访问一次就重新入队
		// 一次——上游越不稳，重试打得越狠。这里跟 ScheduleDueRefreshes 一样在入队时就推后，
		// 把节奏交还给调度器；用户手动刷新走的是别的 reason，不受影响。
		_, _ = store.database.Exec(ctx,
			`UPDATE media SET next_refresh_at = NOW() + INTERVAL '24 hours' WHERE douban_id = $1`, doubanID)
	}
	return jobID, err
}

func (store *PostgresStore) EnqueueMediaRefresh(ctx context.Context, mediaID int, reason string, requestedBy int) (int, error) {
	if mediaID <= 0 {
		return 0, fmt.Errorf("invalid media ID %d", mediaID)
	}
	var doubanID string
	if err := store.database.QueryRow(ctx, `SELECT douban_id FROM media WHERE id = $1 AND douban_id <> ''`, mediaID).Scan(&doubanID); err != nil {
		return 0, fmt.Errorf("resolve media refresh identity: %w", err)
	}
	return store.EnqueueRefresh(ctx, doubanID, RefreshProviderDouban, reason, requestedBy)
}

func validRefreshProvider(provider string) bool {
	switch provider {
	case RefreshProviderDouban, RefreshProviderReviews, RefreshProviderTMDB, RefreshProviderEmbedding:
		return true
	default:
		return false
	}
}

func (store *PostgresStore) ScheduleDueRefreshes(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 20
	}
	_, err := store.database.Exec(ctx, `WITH due AS (
    SELECT id, douban_id FROM media
    WHERE douban_id <> '' AND next_refresh_at IS NOT NULL AND next_refresh_at <= NOW()
    ORDER BY next_refresh_at, id LIMIT $1
), queued AS (
    INSERT INTO worker_jobs (task_type, subject_key, payload, reason, status, available_at)
    SELECT 'douban_metadata', douban_id, JSONB_BUILD_OBJECT('douban_id', douban_id), 'scheduled', 'pending', NOW() FROM due
    ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO NOTHING
    RETURNING subject_key
)
UPDATE media SET next_refresh_at = NOW() + INTERVAL '24 hours'
WHERE douban_id IN (SELECT subject_key FROM queued)`, limit)
	if err != nil {
		return fmt.Errorf("schedule due metadata refreshes: %w", err)
	}
	return nil
}

// ScheduleActiveContentRefreshes 为近期真正播放过、但资料已超过三天未刷新的媒体入队。
// 这项查询必须由 Worker 使用的 catalog Store 实现，否则统一 Dispatcher 无法发现该能力。
func (store *PostgresStore) ScheduleActiveContentRefreshes(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 10
	}
	_, err := store.database.Exec(ctx, `WITH active AS (
    SELECT DISTINCT event.media_id FROM playback_attempt_events event
    WHERE event.event_type = 'played_10s'
      AND event.created_at >= NOW() - INTERVAL '24 hours'
      AND event.media_id > 0
    LIMIT $1
), stale AS (
    SELECT m.douban_id FROM active
    JOIN media m ON m.id = active.media_id
    WHERE m.douban_id <> ''
      AND (m.last_metadata_sync_at IS NULL OR m.last_metadata_sync_at < NOW() - INTERVAL '3 days')
)
INSERT INTO worker_jobs (task_type, subject_key, payload, reason, status, available_at)
SELECT 'douban_metadata', douban_id, JSONB_BUILD_OBJECT('douban_id', douban_id), 'active_content', 'pending', NOW() FROM stale
ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO NOTHING`, limit)
	if err != nil {
		return fmt.Errorf("schedule active content refreshes: %w", err)
	}
	return nil
}

type RefreshHandler struct {
	queue     RefreshQueue
	fetcher   Fetcher
	vectors   VectorEnricher
	reviews   ReviewFetcher
	backdrops BackdropSyncer
}

type RefreshHandlerOption func(*RefreshHandler)

func WithRefreshReviews(fetcher ReviewFetcher) RefreshHandlerOption {
	return func(handler *RefreshHandler) { handler.reviews = fetcher }
}

func WithRefreshBackdrops(syncer BackdropSyncer) RefreshHandlerOption {
	return func(handler *RefreshHandler) { handler.backdrops = syncer }
}

func NewRefreshHandler(queue RefreshQueue, fetcher Fetcher, vectors VectorEnricher, options ...RefreshHandlerOption) *RefreshHandler {
	handler := &RefreshHandler{queue: queue, fetcher: fetcher, vectors: vectors}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (handler *RefreshHandler) Handle(ctx context.Context, job workqueue.Job) error {
	doubanID := job.SubjectKey
	switch job.TaskType {
	case RefreshProviderDouban:
		if handler.fetcher == nil {
			return workqueue.Terminal(fmt.Errorf("Douban metadata refresher is not configured"))
		}
		if err := handler.fetcher.Fetch(ctx, doubanID, true); err != nil {
			return err
		}
		if handler.backdrops != nil {
			if _, err := handler.queue.EnqueueRefresh(ctx, doubanID, RefreshProviderTMDB, job.Reason, job.RequestedBy); err != nil {
				return err
			}
		}
		if handler.vectors != nil {
			if _, err := handler.queue.EnqueueRefresh(ctx, doubanID, RefreshProviderEmbedding, job.Reason, job.RequestedBy); err != nil {
				return err
			}
		}
		return nil
	case RefreshProviderReviews:
		if handler.reviews == nil {
			return workqueue.Terminal(fmt.Errorf("Douban review refresher is not configured"))
		}
		return handler.reviews.FetchReviews(ctx, doubanID)
	case RefreshProviderTMDB:
		if handler.backdrops == nil {
			return workqueue.Terminal(fmt.Errorf("TMDB refresher is not configured"))
		}
		return handler.backdrops.SyncBackdrops(ctx, doubanID)
	case RefreshProviderEmbedding:
		if handler.vectors == nil {
			return workqueue.Terminal(fmt.Errorf("embedding refresher is not configured"))
		}
		return handler.vectors.Enrich(ctx, doubanID)
	default:
		return workqueue.Terminal(fmt.Errorf("unsupported metadata refresh provider %q", job.TaskType))
	}
}

func (handler *RefreshHandler) Schedule(ctx context.Context, _ workqueue.Job) error {
	store, ok := handler.queue.(interface {
		ScheduleDueRefreshes(context.Context, int) error
		ScheduleActiveContentRefreshes(context.Context, int) error
	})
	if !ok {
		return nil
	}
	if err := store.ScheduleDueRefreshes(ctx, 20); err != nil {
		return err
	}
	return store.ScheduleActiveContentRefreshes(ctx, 10)
}
