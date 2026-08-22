package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

// 元数据刷新的任务类型，以及自动触发刷新的原因标识。
const (
	RefreshProviderDouban    = "douban_metadata"
	RefreshProviderReviews   = "douban_reviews"
	RefreshProviderTMDB      = "tmdb"
	RefreshProviderEmbedding = "embedding"

	// 下面这些 reason 都是详情页按「某个字段还是空的」自动触发的，
	// 需要入队冷却，理由见 autoRefreshCooldowns。
	RefreshReasonPartialMetadata  = "partial_metadata"
	RefreshReasonMissingMetadata  = "missing_metadata"
	RefreshReasonMissingReviews   = "missing_reviews"
	RefreshReasonMissingBackdrops = "missing_backdrops"
	RefreshReasonMissingEmbedding = "missing_embedding"
	RefreshReasonSearchDiscovery  = "search_discovery"
)

// autoRefreshCooldowns 是详情页自动入队的冷却时间。这些入队条件都挂在「某个字段还是空的」
// 上，而那个字段只有抓成功才会被填上：上游一坏、或者上游本来就没有这份数据
// （TMDB 大量条目没有剧照、冷门片没有短评），条件就永远成立，页面每被访问一次就重新
// 入队一次。pending/running 唯一索引只保证同时只有一个任务，挡不住「刚结束就立刻再排一个」。
// 不在这张表里的 reason 行为不变：调度器入队时自己会推进 next_refresh_at，
// IMDb 回填是查到新映射才触发的事件，用户手动刷新则必须立即生效。
var autoRefreshCooldowns = map[string]time.Duration{
	RefreshReasonPartialMetadata:  24 * time.Hour,
	RefreshReasonMissingReviews:   24 * time.Hour,
	RefreshReasonMissingEmbedding: 24 * time.Hour,
	// 详情页缺主资料时整页都渲染不出来，冷却给短一点，让用户重试还有机会。
	RefreshReasonMissingMetadata: time.Hour,
	// TMDB 没有剧照的条目占比很高，而且是永久状态，一周内不必再问第二次。
	RefreshReasonMissingBackdrops: 7 * 24 * time.Hour,
}

// RefreshQueue 是资料刷新入队接口。
type RefreshQueue interface {
	EnqueueRefresh(ctx context.Context, doubanID, provider, reason string, requestedBy int) (int, error)
}

// MediaRefreshQueue 支持按 media.id 入队（后台/接口用，前台一般用豆瓣 ID）。
type MediaRefreshQueue interface {
	EnqueueMediaRefresh(ctx context.Context, mediaID int, reason string, requestedBy int) (int, error)
}

// TMDBRefreshChecker 判断一部影片是否还缺首次 TMDB 资料采集。
type TMDBRefreshChecker interface {
	NeedsTMDBRefresh(ctx context.Context, doubanID string) (bool, error)
}

// EnqueueRefresh 把一次资料刷新放进 worker_jobs。返回的 job id 为 0 表示被冷却挡下了，不是失败。
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
	if skip, _ := store.alreadyComplete(ctx, provider, doubanID); skip {
		return 0, nil
	}
	if cooling, err := store.coolingDown(ctx, provider, doubanID, reason); err != nil {
		return 0, err
	} else if cooling {
		return 0, nil
	}
	priority := 0
	if provider == RefreshProviderDouban {
		priority = 5
	}
	return workqueue.NewPostgresStore(store.database).Enqueue(ctx, workqueue.Spec{
		TaskType: provider, SubjectKey: doubanID, Payload: map[string]string{"douban_id": doubanID},
		Reason: reason, RequestedBy: requestedBy, Priority: priority,
	})
}

// coolingDown 判断这个对象最近是否已经跑完过一轮同类任务。只看终态行：
// pending/running 由唯一偏索引兜住，不该也不需要再被冷却挡一次。
func (store *PostgresStore) coolingDown(ctx context.Context, provider, doubanID, reason string) (bool, error) {
	cooldown, tracked := autoRefreshCooldowns[reason]
	if !tracked {
		return false, nil
	}
	var recent bool
	if err := store.database.QueryRow(ctx, `SELECT EXISTS (
    SELECT 1 FROM worker_jobs
    WHERE task_type = $1 AND subject_key = $2 AND status IN ('completed', 'failed')
      AND finished_at IS NOT NULL AND finished_at > NOW() - make_interval(secs => $3))`,
		provider, doubanID, cooldown.Seconds()).Scan(&recent); err != nil {
		return false, fmt.Errorf("check refresh cooldown: %w", err)
	}
	return recent, nil
}

// alreadyComplete 检查该 provider 的数据是否已经存在，无需重复采集。
func (store *PostgresStore) alreadyComplete(ctx context.Context, provider, doubanID string) (bool, error) {
	var query string
	switch provider {
	case RefreshProviderDouban:
		query = `SELECT completeness_score >= 70 AND metadata_status <> 'partial' FROM media WHERE douban_id = $1`
	case RefreshProviderReviews:
		query = `SELECT reviews_json <> '' AND reviews_json <> '[]' FROM media WHERE douban_id = $1`
	case RefreshProviderTMDB:
		query = `SELECT backdrops <> '' AND EXISTS (SELECT 1 FROM media_external_ids WHERE media_id = m.id AND provider = 'tmdb') FROM media m WHERE m.douban_id = $1`
	case RefreshProviderEmbedding:
		query = `SELECT semantic_hash <> '' FROM media WHERE douban_id = $1`
	default:
		return false, nil
	}
	var done bool
	if err := store.database.QueryRow(ctx, query, doubanID).Scan(&done); err != nil {
		return false, err
	}
	return done, nil
}

// EnqueueMediaRefresh 先把 media.id 换成豆瓣 ID，再走 EnqueueRefresh。
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

// NeedsTMDBRefresh 在剧照为空或尚无 TMDB 映射时返回 true。
// IMDb 映射是当前 TMDB 采集器的查询前提，没有时交给 imdb_backfill 处理。
func (store *PostgresStore) NeedsTMDBRefresh(ctx context.Context, doubanID string) (bool, error) {
	var needed bool
	if err := store.database.QueryRow(ctx, `SELECT EXISTS (
    SELECT 1 FROM media m
    WHERE m.douban_id = $1
      AND EXISTS (SELECT 1 FROM media_external_ids WHERE media_id = m.id AND provider = 'imdb')
      AND (m.backdrops = ''
        OR NOT EXISTS (SELECT 1 FROM media_external_ids WHERE media_id = m.id AND provider = 'tmdb'))
)`, doubanID).Scan(&needed); err != nil {
		return false, fmt.Errorf("check TMDB refresh state: %w", err)
	}
	return needed, nil
}

// validRefreshProvider 只允许四种已知的刷新来源，防止任意字符串被写进任务表。
func validRefreshProvider(provider string) bool {
	switch provider {
	case RefreshProviderDouban, RefreshProviderReviews, RefreshProviderTMDB, RefreshProviderEmbedding:
		return true
	default:
		return false
	}
}

// ScheduleDueRefreshes 把到期且资料不完整的影片批量入队。
// 资料已完整的影片清除 next_refresh_at，不再轮转。
func (store *PostgresStore) ScheduleDueRefreshes(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 20
	}
	_, err := store.database.Exec(ctx, `WITH due AS (
    SELECT id, douban_id, (metadata_status = 'partial' OR completeness_score < 70) AS incomplete
    FROM media
    WHERE douban_id <> '' AND next_refresh_at IS NOT NULL AND next_refresh_at <= NOW()
    ORDER BY next_refresh_at, id LIMIT $1
), skip_complete AS (
    UPDATE media SET next_refresh_at = NULL
    WHERE id IN (SELECT id FROM due WHERE NOT incomplete)
), queued AS (
    INSERT INTO worker_jobs (task_type, subject_key, payload, reason, status, available_at)
    SELECT 'douban_metadata', douban_id, JSONB_BUILD_OBJECT('douban_id', douban_id), 'scheduled', 'pending', NOW()
    FROM due WHERE incomplete
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

// ScheduleActiveContentRefreshes 为近期真正播放过、资料不完整或长期未刷新的媒体入队。
// 资料已完整（completeness_score >= 70 且非 partial）的影片跳过，避免无谓刷新。
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
      AND (m.metadata_status = 'partial' OR m.completeness_score < 70)
)
INSERT INTO worker_jobs (task_type, subject_key, payload, reason, status, available_at)
SELECT 'douban_metadata', douban_id, JSONB_BUILD_OBJECT('douban_id', douban_id), 'active_content', 'pending', NOW() FROM stale
ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO NOTHING`, limit)
	if err != nil {
		return fmt.Errorf("schedule active content refreshes: %w", err)
	}
	return nil
}

// RefreshHandler 是资料刷新任务的执行器，四种 provider 走同一个 Handle 分发。
type RefreshHandler struct {
	queue     RefreshQueue
	fetcher   Fetcher
	vectors   VectorEnricher
	reviews   ReviewFetcher
	backdrops BackdropSyncer
}

// RefreshHandlerOption 是刷新执行器的可选装配项。
type RefreshHandlerOption func(*RefreshHandler)

// WithRefreshReviews 注入短评抓取。
func WithRefreshReviews(fetcher ReviewFetcher) RefreshHandlerOption {
	return func(handler *RefreshHandler) { handler.reviews = fetcher }
}

// WithRefreshBackdrops 注入 TMDB 剧照同步。
func WithRefreshBackdrops(syncer BackdropSyncer) RefreshHandlerOption {
	return func(handler *RefreshHandler) { handler.backdrops = syncer }
}

// NewRefreshHandler 创建刷新执行器。
func NewRefreshHandler(queue RefreshQueue, fetcher Fetcher, vectors VectorEnricher, options ...RefreshHandlerOption) *RefreshHandler {
	handler := &RefreshHandler{queue: queue, fetcher: fetcher, vectors: vectors}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Handle 执行一个刷新任务。豆瓣主资料抓完后，只在首次 TMDB 资料确实缺失时派生 TMDB 任务；
// 剧照是该任务的附带结果，不会因为已有资料而反复刷新。
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
			checker, ok := handler.queue.(TMDBRefreshChecker)
			if ok {
				needed, err := checker.NeedsTMDBRefresh(ctx, doubanID)
				if err != nil {
					return err
				}
				if needed {
					if _, err := handler.queue.EnqueueRefresh(ctx, doubanID, RefreshProviderTMDB, job.Reason, job.RequestedBy); err != nil {
						return err
					}
				}
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

// Schedule 是定时入口：把到期的和近期有人播放过的媒体排进队列。
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
