package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type MetricsReader interface {
	Snapshot(ctx context.Context) (MetricsSnapshot, error)
}

type MetricsSnapshot struct {
	GeneratedAt string                       `json:"generated_at"`
	WindowHours int                          `json:"window_hours"`
	Media       MediaMetrics                 `json:"media"`
	Matches     MatchMetrics                 `json:"matches"`
	Search      SearchMetrics                `json:"search"`
	History     HistoryMetrics               `json:"history"`
	Playback    PlaybackMetrics              `json:"playback"`
	Refresh     RefreshMetrics               `json:"refresh"`
	Resources   ResourceMetrics              `json:"resources"`
	Popularity  map[string]PopularityMetrics `json:"popularity"`
}

type MediaMetrics struct {
	Total              int64 `json:"total"`
	CompletenessLow    int64 `json:"completeness_low"`
	CompletenessMedium int64 `json:"completeness_medium"`
	CompletenessHigh   int64 `json:"completeness_high"`
}

type MatchMetrics struct {
	Exact     int64 `json:"exact"`
	Auto      int64 `json:"automatic"`
	Review    int64 `json:"review"`
	Conflict  int64 `json:"conflict"`
	Unmatched int64 `json:"unmatched_resources"`
}

type SearchMetrics struct {
	OK      int64 `json:"ok"`
	Empty   int64 `json:"empty"`
	Timeout int64 `json:"timeout"`
	Error   int64 `json:"error"`
}

type HistoryMetrics struct {
	Total        int64 `json:"total"`
	Active       int64 `json:"active"`
	WithMedia    int64 `json:"with_media"`
	ResourceOnly int64 `json:"resource_only"`
	Tombstones   int64 `json:"tombstones"`
	SyncEvents   int64 `json:"sync_events"`
}

type PlaybackMetrics struct {
	Attempts               int64   `json:"attempts"`
	FirstFrames            int64   `json:"first_frames"`
	PlayedTenSeconds       int64   `json:"played_10s"`
	FatalErrors            int64   `json:"fatal_errors"`
	SourceSwitches         int64   `json:"source_switches"`
	SuccessfulSwitches     int64   `json:"successful_switches"`
	WrongUnitSessions      int64   `json:"wrong_unit_sessions"`
	FirstFrameRate         float64 `json:"first_frame_rate"`
	PlayedTenSecondsRate   float64 `json:"played_10s_rate"`
	SwitchSuccessRate      float64 `json:"switch_success_rate"`
	StartupP50Milliseconds int64   `json:"startup_p50_ms"`
	StartupP90Milliseconds int64   `json:"startup_p90_ms"`
}

type RefreshMetrics struct {
	DueMedia             int64 `json:"due_media"`
	Pending              int64 `json:"pending"`
	Running              int64 `json:"running"`
	Failed               int64 `json:"failed"`
	OldestPendingSeconds int64 `json:"oldest_pending_seconds"`
	ProviderSuccess      int64 `json:"provider_success"`
	ProviderFailure      int64 `json:"provider_failure"`
	ProviderUnchanged    int64 `json:"provider_unchanged"`
}

type ResourceMetrics struct {
	Active           int64 `json:"active"`
	Cold             int64 `json:"cold"`
	Removed          int64 `json:"removed"`
	Broken           int64 `json:"broken"`
	PreviewBatches   int64 `json:"preview_batches"`
	AppliedBatches   int64 `json:"applied_batches"`
	DryRunApplyDelta int64 `json:"dry_run_apply_delta"`
}

type PopularityMetrics struct {
	ItemCount  int64          `json:"item_count"`
	AgeSeconds int64          `json:"age_seconds"`
	ExpiresIn  int64          `json:"expires_in_seconds"`
	Sources    map[string]int `json:"sources"`
}

type MetricsStore struct {
	database database.Executor
	mu       sync.Mutex
	cached   MetricsSnapshot
	expires  time.Time
	now      func() time.Time
	cacheTTL time.Duration
}

func NewMetricsStore(executor database.Executor) *MetricsStore {
	return &MetricsStore{database: executor, now: time.Now, cacheTTL: 15 * time.Second}
}

func (store *MetricsStore) Snapshot(ctx context.Context) (MetricsSnapshot, error) {
	if store == nil || store.database == nil {
		return MetricsSnapshot{GeneratedAt: time.Now().UTC().Format(time.RFC3339), WindowHours: 24,
			Popularity: make(map[string]PopularityMetrics)}, nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.now().Before(store.expires) {
		return store.cached, nil
	}
	var payload []byte
	if err := store.database.QueryRow(ctx, metricsSnapshotSQL).Scan(&payload); err != nil {
		return MetricsSnapshot{}, fmt.Errorf("query operations metrics: %w", err)
	}
	var snapshot MetricsSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return MetricsSnapshot{}, fmt.Errorf("decode operations metrics: %w", err)
	}
	if snapshot.Popularity == nil {
		snapshot.Popularity = make(map[string]PopularityMetrics)
	}
	store.cached = snapshot
	store.expires = store.now().Add(store.cacheTTL)
	return snapshot, nil
}

// DeleteExpiredTelemetry 清理只在近期窗口内被读取的遥测数据。
// 这三张表原本没有任何保留策略，只增不减：playback_attempt_events 每次播放尝试最多写 9 行、
// 带 3 个索引，还被 ScheduleActiveContentRefreshes 定期扫描，表一大调度先慢下来。
// 所有读取方的窗口都不超过 7 天（activity_popular 7 天、rollup 全部 7 天、
// refresh 与 metrics 24 小时），rollup 又是和事件同一条语句里算出来的、不依赖原始事件，
// 所以按 before 删除历史行不改变任何查询结果。
// popularity 只读「每个 media_type 最新的 ready 快照」，但那一行必须留着：
// 运维面板靠它显示上次快照时间，删了会让面板误报从未生成。
// budget 是每张表每次清理的上限。清理任务 24 小时才跑一次，所以不能只删一批就收工，
// 否则存量积压永远排不空；但一次性删几百万行又会长时间持锁。这里分小批循环，
// 每条 DELETE 都只碰 telemetryDeleteChunk 行。
const telemetryDeleteChunk = 20000

func (store *MetricsStore) DeleteExpiredTelemetry(ctx context.Context, before time.Time, budget int) (int, error) {
	if store == nil || store.database == nil {
		return 0, nil
	}
	if budget < 1 {
		budget = telemetryDeleteChunk
	}
	total := 0
	for _, table := range []struct {
		name      string
		statement string
	}{
		{"playback events", `WITH expired AS (
    SELECT id FROM playback_attempt_events WHERE created_at < $1 ORDER BY id LIMIT $2
)
DELETE FROM playback_attempt_events events USING expired WHERE events.id = expired.id`},
		{"playback rollups", `WITH expired AS (
    SELECT bucket, candidate_id FROM playback_quality_rollups WHERE bucket < $1 LIMIT $2
)
DELETE FROM playback_quality_rollups rollups USING expired
WHERE rollups.bucket = expired.bucket AND rollups.candidate_id = expired.candidate_id`},
	} {
		for deleted := 0; deleted < budget; {
			chunk := min(telemetryDeleteChunk, budget-deleted)
			affected, err := store.database.Exec(ctx, table.statement, before, chunk)
			if err != nil {
				return total, fmt.Errorf("delete expired %s: %w", table.name, err)
			}
			deleted, total = deleted+int(affected), total+int(affected)
			if int(affected) < chunk {
				break
			}
		}
	}
	// 快照运行记录每天只有个位数行，一条语句就删干净了。
	// popularity_snapshots 通过 run_id 外键级联删除，不必单独处理。
	runs, err := store.database.Exec(ctx, `WITH latest AS (
    SELECT DISTINCT ON (media_type) id FROM popularity_snapshot_runs
    WHERE status = 'ready' ORDER BY media_type, generated_at DESC
)
DELETE FROM popularity_snapshot_runs
WHERE generated_at < $1 AND id NOT IN (SELECT id FROM latest)`, before)
	if err != nil {
		return total, fmt.Errorf("delete expired popularity snapshots: %w", err)
	}
	return total + int(runs), nil
}

const metricsSnapshotSQL = `WITH event_window AS (
    SELECT * FROM playback_attempt_events WHERE created_at >= NOW() - INTERVAL '24 hours'
), event_totals AS (
    SELECT COUNT(DISTINCT attempt_id) FILTER (WHERE event_type = 'attempt_started') AS attempts,
           COUNT(DISTINCT attempt_id) FILTER (WHERE event_type = 'first_frame') AS first_frames,
           COUNT(DISTINCT attempt_id) FILTER (WHERE event_type = 'played_10s') AS played_10s,
           COUNT(DISTINCT attempt_id) FILTER (WHERE event_type = 'fatal_error') AS fatal_errors
    FROM event_window
), switched_sessions AS (
    SELECT candidate_session_id, MIN(created_at) FILTER (WHERE event_type = 'source_switched') AS switched_at
    FROM event_window WHERE candidate_session_id <> '' GROUP BY candidate_session_id
), switch_totals AS (
    SELECT COUNT(*) FILTER (WHERE switched_at IS NOT NULL) AS switched,
           COUNT(*) FILTER (WHERE switched_at IS NOT NULL AND EXISTS (
               SELECT 1 FROM event_window success
               WHERE success.candidate_session_id = switched_sessions.candidate_session_id
                 AND success.event_type = 'played_10s' AND success.created_at > switched_sessions.switched_at
           )) AS successful
    FROM switched_sessions
), wrong_units AS (
    SELECT COUNT(*) AS total FROM (
        SELECT candidate_session_id FROM event_window WHERE candidate_session_id <> ''
        GROUP BY candidate_session_id HAVING COUNT(DISTINCT media_unit_id) > 1
    ) invalid
), latest_popularity AS (
    SELECT DISTINCT ON (media_type) media_type, item_count, source_status, generated_at, expires_at
    FROM popularity_snapshot_runs WHERE status = 'ready'
    ORDER BY media_type, generated_at DESC
)
SELECT JSONB_BUILD_OBJECT(
    'generated_at', TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
    'window_hours', 24,
    'media', JSONB_BUILD_OBJECT(
        'total', (SELECT COUNT(*) FROM media),
        'completeness_low', (SELECT COUNT(*) FROM media WHERE completeness_score < 40),
        'completeness_medium', (SELECT COUNT(*) FROM media WHERE completeness_score >= 40 AND completeness_score < 70),
        'completeness_high', (SELECT COUNT(*) FROM media WHERE completeness_score >= 70)
    ),
    'matches', JSONB_BUILD_OBJECT(
        'exact', (SELECT COUNT(*) FROM resource_media_links WHERE confidence = 1 AND matched_by IN ('douban_id', 'external_id')),
        'automatic', (SELECT COUNT(*) FROM resource_media_links WHERE confidence >= 0.88 AND matched_by NOT IN ('manual', 'douban_id', 'external_id')),
        'review', (SELECT COUNT(*) FROM resource_match_candidates WHERE status = 'review'),
        'conflict', (SELECT COUNT(*) FROM resource_match_candidates WHERE status = 'rejected'),
        'unmatched_resources', (SELECT COUNT(*) FROM vod_items resource WHERE NOT EXISTS (SELECT 1 FROM resource_media_links link WHERE link.source_key = resource.source_key AND link.vod_id = resource.vod_id))
    ),
    'search', JSONB_BUILD_OBJECT(
        'ok', (SELECT COALESCE(SUM(ok_count), 0) FROM site_stats WHERE bucket >= NOW() - INTERVAL '24 hours'),
        'empty', (SELECT COALESCE(SUM(empty_count), 0) FROM site_stats WHERE bucket >= NOW() - INTERVAL '24 hours'),
        'timeout', (SELECT COALESCE(SUM(timeout_count), 0) FROM site_stats WHERE bucket >= NOW() - INTERVAL '24 hours'),
        'error', (SELECT COALESCE(SUM(error_count), 0) FROM site_stats WHERE bucket >= NOW() - INTERVAL '24 hours')
    ),
	'history', JSONB_BUILD_OBJECT(
		'total', (SELECT COUNT(*) FROM playback_positions),
		'active', (SELECT COUNT(*) FROM playback_positions WHERE deleted_at IS NULL),
		'with_media', (SELECT COUNT(*) FROM playback_positions WHERE media_id IS NOT NULL),
		'resource_only', (SELECT COUNT(*) FROM playback_positions WHERE media_id IS NULL),
        'tombstones', (SELECT COUNT(*) FROM playback_positions WHERE deleted_at IS NOT NULL),
        'sync_events', (SELECT COUNT(*) FROM history_sync_events WHERE created_at >= NOW() - INTERVAL '24 hours')
    ),
    'playback', JSONB_BUILD_OBJECT(
        'attempts', COALESCE((SELECT attempts FROM event_totals), 0),
        'first_frames', COALESCE((SELECT first_frames FROM event_totals), 0),
        'played_10s', COALESCE((SELECT played_10s FROM event_totals), 0),
        'fatal_errors', COALESCE((SELECT fatal_errors FROM event_totals), 0),
        'source_switches', COALESCE((SELECT switched FROM switch_totals), 0),
        'successful_switches', COALESCE((SELECT successful FROM switch_totals), 0),
        'wrong_unit_sessions', COALESCE((SELECT total FROM wrong_units), 0),
        'first_frame_rate', COALESCE((SELECT ROUND(100.0 * first_frames / NULLIF(attempts, 0), 2) FROM event_totals), 0),
        'played_10s_rate', COALESCE((SELECT ROUND(100.0 * played_10s / NULLIF(attempts, 0), 2) FROM event_totals), 0),
        'switch_success_rate', COALESCE((SELECT ROUND(100.0 * successful / NULLIF(switched, 0), 2) FROM switch_totals), 0),
        'startup_p50_ms', COALESCE((SELECT PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY elapsed_ms)::bigint FROM event_window WHERE event_type = 'first_frame'), 0),
        'startup_p90_ms', COALESCE((SELECT PERCENTILE_CONT(0.90) WITHIN GROUP (ORDER BY elapsed_ms)::bigint FROM event_window WHERE event_type = 'first_frame'), 0)
    ),
    'refresh', JSONB_BUILD_OBJECT(
        'due_media', (SELECT COUNT(*) FROM media WHERE next_refresh_at IS NOT NULL AND next_refresh_at <= NOW()),
		'pending', (SELECT COUNT(*) FROM worker_jobs WHERE status = 'pending'),
		'running', (SELECT COUNT(*) FROM worker_jobs WHERE status = 'running'),
		'failed', (SELECT COUNT(*) FROM worker_jobs WHERE status = 'failed'),
		'oldest_pending_seconds', COALESCE((SELECT EXTRACT(EPOCH FROM NOW() - MIN(created_at))::bigint FROM worker_jobs WHERE status = 'pending'), 0),
        'provider_success', (SELECT COUNT(*) FROM media_source_snapshots WHERE error_message = ''),
        'provider_failure', (SELECT COUNT(*) FROM media_source_snapshots WHERE error_message <> ''),
        'provider_unchanged', (SELECT COUNT(*) FROM media_source_snapshots WHERE unchanged_count > 0)
    ),
    'resources', JSONB_BUILD_OBJECT(
        'active', (SELECT COUNT(*) FROM vod_items WHERE resource_status = 'active'),
        'cold', (SELECT COUNT(*) FROM vod_items WHERE resource_status = 'cold'),
        'removed', (SELECT COUNT(*) FROM vod_items WHERE resource_status IN ('removed', 'retired')),
        'broken', (SELECT COUNT(*) FROM vod_items WHERE resource_status IN ('broken', 'stale')),
        'preview_batches', (SELECT COUNT(*) FROM resource_lifecycle_batches WHERE status = 'previewed'),
        'applied_batches', (SELECT COUNT(*) FROM resource_lifecycle_batches WHERE status = 'applied'),
        'dry_run_apply_delta', (SELECT COALESCE(SUM(ABS(candidate_count - COALESCE(applied_count, 0))), 0) FROM resource_lifecycle_batches WHERE status = 'applied')
    ),
    'popularity', COALESCE((SELECT JSONB_OBJECT_AGG(media_type, JSONB_BUILD_OBJECT(
        'item_count', item_count,
        'age_seconds', GREATEST(0, EXTRACT(EPOCH FROM NOW() - generated_at)::bigint),
        'expires_in_seconds', EXTRACT(EPOCH FROM expires_at - NOW())::bigint,
        'sources', source_status
    )) FROM latest_popularity), '{}'::jsonb)
)`
