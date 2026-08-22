package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

var ErrEmptyPopularitySnapshot = errors.New("popularity snapshot has no items")

// popularitySnapshotSize 一份快照固定 50 条，不足就算发布失败。
const popularitySnapshotSize = 50

// popularitySnapshotDatabase 是快照存储需要的数据库能力。
type popularitySnapshotDatabase interface {
	database.Executor
	database.Beginner
}

// PopularitySnapshotStore 保存不可变的热门快照；media_id 只在能够匹配规范媒体时填写。
// 只有同一事务写完全部条目并将批次标记为 ready 后，该批次才对读取方可见。
type PopularitySnapshotStore struct {
	database popularitySnapshotDatabase
}

// NewPopularitySnapshotStore 创建快照存储。
func NewPopularitySnapshotStore(db popularitySnapshotDatabase) *PopularitySnapshotStore {
	return &PopularitySnapshotStore{database: db}
}

// Replace 发布一份新快照：去重截取前 50 条 → 在一个事务里写完再标记 ready。
// 写到一半失败会整体回滚，读取方永远看不到半份榜单。
func (store *PopularitySnapshotStore) Replace(ctx context.Context, mediaType string, subjects []PopularSubject, ttl time.Duration) error {
	if store == nil || store.database == nil {
		return fmt.Errorf("popularity snapshot database is not configured")
	}
	switch mediaType {
	case "movie", "tv", "show", "cartoon":
	default:
		return fmt.Errorf("unsupported media type %q", mediaType)
	}
	if ttl <= 0 {
		return fmt.Errorf("popularity snapshot ttl must be positive")
	}
	if len(subjects) == 0 {
		return fmt.Errorf("%w for media type %q: source returned no subjects", ErrEmptyPopularitySnapshot, mediaType)
	}
	subjects = mergePopularSubjects(subjects, nil, popularitySnapshotSize)
	if len(subjects) == 0 {
		return fmt.Errorf("%w for media type %q", ErrEmptyPopularitySnapshot, mediaType)
	}
	if len(subjects) < popularitySnapshotSize {
		slog.Warn("popularity snapshot is incomplete", "media_type", mediaType, "count", len(subjects), "expected", popularitySnapshotSize)
	}
	mediaIDs := store.lookupMediaIDs(ctx, subjects)
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin popularity snapshot: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))
	var runID int64
	if err := transaction.QueryRow(ctx, `INSERT INTO popularity_snapshot_runs
(media_type, status, source_status, item_count, generated_at, expires_at)
VALUES ($1, 'building', '{}'::jsonb, 0, NOW(), NOW() + $2::interval)
RETURNING id`, mediaType, intervalLiteral(ttl)).Scan(&runID); err != nil {
		return fmt.Errorf("create popularity snapshot run: %w", err)
	}
	for index, subject := range subjects {
		payload, err := json.Marshal(subject)
		if err != nil {
			return fmt.Errorf("encode popularity subject: %w", err)
		}
		var mediaID any
		if id, ok := mediaIDs[strings.TrimSpace(subject.ID)]; ok {
			mediaID = id
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO popularity_snapshots
(run_id, media_id, rank, subject_payload, generated_at)
VALUES ($1, $2, $3, $4::jsonb, NOW())`,
			runID, mediaID, index+1, string(payload)); err != nil {
			return fmt.Errorf("insert popularity snapshot item: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `UPDATE popularity_snapshot_runs
SET status = 'ready', item_count = $2, completed_at = NOW()
WHERE id = $1 AND status = 'building'`, runID, len(subjects)); err != nil {
		return fmt.Errorf("publish popularity snapshot: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit popularity snapshot: %w", err)
	}
	return nil
}

// lookupMediaIDs 批量查豆瓣 ID 对应的规范媒体 ID，用于读取快照时 JOIN media 展示最新资料。
func (store *PopularitySnapshotStore) lookupMediaIDs(ctx context.Context, subjects []PopularSubject) map[string]int64 {
	ids := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		if id := strings.TrimSpace(subject.ID); id != "" {
			ids = append(ids, id)
		}
	}
	result := make(map[string]int64)
	if len(ids) == 0 {
		return result
	}
	rows, err := store.database.Query(ctx, `SELECT id, douban_id FROM media WHERE douban_id = ANY($1::text[])`, ids)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var doubanID string
		if err := rows.Scan(&id, &doubanID); err == nil {
			result[doubanID] = id
		}
	}
	return result
}

// Popular 读取当前生效的快照（最近一份 ready 且未过期的），并用 media 表的最新资料覆盖标题海报。
func (store *PopularitySnapshotStore) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	if store == nil || store.database == nil {
		return nil, fmt.Errorf("popularity snapshot database is not configured")
	}
	rows, err := store.database.Query(ctx, `SELECT snapshot.subject_payload,
COALESCE(media.douban_id, ''), COALESCE(media.title, ''), COALESCE(media.year, ''),
COALESCE(media.poster, ''), COALESCE(media.rating_douban, 0)
FROM popularity_snapshot_runs run
JOIN popularity_snapshots snapshot ON snapshot.run_id = run.id
LEFT JOIN media ON media.id = snapshot.media_id
WHERE run.id = (
    SELECT latest.id FROM popularity_snapshot_runs latest
    WHERE latest.media_type = $1 AND latest.status = 'ready' AND latest.expires_at > NOW()
    ORDER BY latest.generated_at DESC LIMIT 1
)
ORDER BY snapshot.rank`, mediaType)
	if err != nil {
		return nil, fmt.Errorf("query popularity snapshot: %w", err)
	}
	defer rows.Close()
	items := make([]PopularSubject, 0)
	for rows.Next() {
		var payload []byte
		var doubanID, title, year, poster string
		var rating float64
		if err := rows.Scan(&payload, &doubanID, &title, &year, &poster, &rating); err != nil {
			return nil, fmt.Errorf("scan popularity snapshot: %w", err)
		}
		var subject PopularSubject
		if err := json.Unmarshal(payload, &subject); err != nil {
			return nil, fmt.Errorf("decode popularity snapshot: %w", err)
		}
		if doubanID != "" {
			subject.ID = doubanID
		}
		if title != "" {
			subject.Title = title
		}
		if year != "" {
			subject.Year = year
		}
		if poster != "" {
			subject.Cover = proxyImagePath(poster)
		}
		if rating > 0 {
			subject.Rate = strconv.FormatFloat(rating, 'f', 1, 64)
		}
		items = append(items, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate popularity snapshot: %w", err)
	}
	return items, nil
}

// SnapshotPopularProvider 优先读快照，快照不足 50 条时用实时来源补齐。
// 首页读的就是它，所以首页正常情况下只查一次数据库，不会现场去抓豆瓣。
type SnapshotPopularProvider struct {
	snapshots *PopularitySnapshotStore
	fallback  PopularProvider
}

// NewSnapshotPopularProvider 创建快照优先的热门来源。
func NewSnapshotPopularProvider(snapshots *PopularitySnapshotStore, fallback PopularProvider) *SnapshotPopularProvider {
	return &SnapshotPopularProvider{snapshots: snapshots, fallback: fallback}
}

// Popular 快照够用就直接返回，不够就和实时来源合并补齐。
func (provider *SnapshotPopularProvider) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	items, err := provider.snapshots.Popular(ctx, mediaType)
	if len(items) >= popularitySnapshotSize {
		return items[:popularitySnapshotSize], nil
	}
	if provider.fallback == nil {
		return items, err
	}
	fallback, fallbackErr := provider.fallback.Popular(ctx, mediaType)
	items = mergePopularSubjects(items, fallback, popularitySnapshotSize)
	if len(items) > 0 {
		return items, nil
	}
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	return nil, err
}

// mergePopularSubjects 按去重键合并两份榜单，最多取 limit 条。
func mergePopularSubjects(primary, supplement []PopularSubject, limit int) []PopularSubject {
	if limit <= 0 {
		return nil
	}
	result := make([]PopularSubject, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, subjects := range [][]PopularSubject{primary, supplement} {
		for _, subject := range subjects {
			identity := popularIdentity(subject)
			if identity == "" {
				continue
			}
			if _, found := seen[identity]; found {
				continue
			}
			seen[identity] = struct{}{}
			result = append(result, subject)
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

// TaskPopularityRefresh 是热门快照刷新任务的类型名。
const TaskPopularityRefresh = "popularity_refresh"

// PopularityRefresher 是定时刷新热门快照的 Worker 任务。
type PopularityRefresher struct {
	store    *PopularitySnapshotStore
	provider PopularProvider
	ttl      time.Duration
}

// NewPopularityRefresher 创建刷新器，快照有效期设为刷新间隔的 2 倍，
// 这样偶尔一次刷新失败也不会让榜单直接过期变空。
func NewPopularityRefresher(store *PopularitySnapshotStore, provider PopularProvider, interval time.Duration) *PopularityRefresher {
	return &PopularityRefresher{store: store, provider: provider, ttl: 2 * interval}
}

// Handle 依次刷新四个分类，单个分类失败不影响其他分类。
func (refresher *PopularityRefresher) Handle(ctx context.Context, _ workqueue.Job) error {
	if refresher == nil || refresher.store == nil || refresher.provider == nil {
		return fmt.Errorf("popularity refresher is not configured")
	}
	var failures []error
	for _, mediaType := range []string{"movie", "tv", "show", "cartoon"} {
		items, err := refresher.provider.Popular(ctx, mediaType)
		if err != nil {
			slog.Warn("popularity source refresh failed", "media_type", mediaType, "error", err)
			failures = append(failures, err)
			continue
		}
		if err := refresher.store.Replace(ctx, mediaType, items, refresher.ttl); err != nil {
			slog.Warn("popularity snapshot publish failed", "media_type", mediaType, "error", err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// intervalLiteral 把时长转成 PostgreSQL interval 字面量。
func intervalLiteral(duration time.Duration) string {
	return strconv.FormatInt(int64(duration/time.Second), 10) + " seconds"
}
