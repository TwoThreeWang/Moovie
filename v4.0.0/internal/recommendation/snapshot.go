package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"github.com/jackc/pgx/v5"
)

const (
	// TaskRefresh 是统一 Worker 队列中的用户推荐刷新任务。
	TaskRefresh = "recommendation_refresh"
	snapshotTTL = 6 * time.Hour
)

// SnapshotStore 保存一位用户一行的完整推荐结果。
type SnapshotStore struct{ database database.Executor }

func NewSnapshotStore(executor database.Executor) *SnapshotStore {
	return &SnapshotStore{database: executor}
}

// Load 返回快照、是否仍新鲜、是否存在。
func (store *SnapshotStore) Load(ctx context.Context, userID int) (forYouData, bool, bool, error) {
	var payload []byte
	var expiresAt time.Time
	if err := store.database.QueryRow(ctx, `SELECT payload, expires_at
FROM user_recommendation_snapshots WHERE user_id = $1`, userID).Scan(&payload, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return forYouData{}, false, false, nil
		}
		return forYouData{}, false, false, fmt.Errorf("load recommendation snapshot: %w", err)
	}
	var data forYouData
	if err := json.Unmarshal(payload, &data); err != nil {
		return forYouData{}, false, false, fmt.Errorf("decode recommendation snapshot: %w", err)
	}
	return data, time.Now().Before(expiresAt), true, nil
}

// Save 原子替换一位用户的完整快照。
func (store *SnapshotStore) Save(ctx context.Context, userID int, data forYouData) error {
	payload, err := json.Marshal(compactForYouData(data))
	if err != nil {
		return fmt.Errorf("encode recommendation snapshot: %w", err)
	}
	if _, err := store.database.Exec(ctx, `INSERT INTO user_recommendation_snapshots
(user_id, payload, generated_at, expires_at) VALUES ($1, $2::jsonb, NOW(), NOW() + $3::interval)
ON CONFLICT (user_id) DO UPDATE SET payload = EXCLUDED.payload,
generated_at = EXCLUDED.generated_at, expires_at = EXCLUDED.expires_at`,
		userID, string(payload), intervalLiteral(snapshotTTL)); err != nil {
		return fmt.Errorf("save recommendation snapshot: %w", err)
	}
	return nil
}

// PopularFallback 从已有的全局热门快照中取可展示影片，供首次生成前立即返回。
func (store *SnapshotStore) PopularFallback(ctx context.Context, limit int) ([]catalog.Movie, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := store.database.Query(ctx, `WITH latest AS (
    SELECT DISTINCT ON (media_type) id, media_type
    FROM popularity_snapshot_runs
    WHERE status = 'ready' AND expires_at > NOW()
    ORDER BY media_type, generated_at DESC
), deduplicated AS (
    SELECT DISTINCT ON (media.id) media.id, media.douban_id, media.title, media.year,
           media.poster, media.rating_douban, media.genres, media.countries, media.summary,
           snapshot.rank
    FROM latest
    JOIN popularity_snapshots snapshot ON snapshot.run_id = latest.id
    JOIN media ON media.id = snapshot.media_id
    ORDER BY media.id, snapshot.rank
)
SELECT id, douban_id, title, year, poster, rating_douban, genres, countries, summary
FROM deduplicated ORDER BY rank, rating_douban DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("load recommendation popularity fallback: %w", err)
	}
	defer rows.Close()
	movies := make([]catalog.Movie, 0, limit)
	for rows.Next() {
		var movie catalog.Movie
		if err := rows.Scan(&movie.ID, &movie.DoubanID, &movie.Title, &movie.Year, &movie.Poster,
			&movie.Rating, &movie.Genres, &movie.Countries, &movie.Summary); err != nil {
			return nil, fmt.Errorf("scan recommendation popularity fallback: %w", err)
		}
		movies = append(movies, movie)
	}
	return movies, rows.Err()
}

// Refresher 在 Worker 中计算并发布用户推荐快照。
type Refresher struct {
	store   *SnapshotStore
	service *Service
}

func NewRefresher(store *SnapshotStore, service *Service) *Refresher {
	return &Refresher{store: store, service: service}
}

func (refresher *Refresher) Refresh(ctx context.Context, userID int) error {
	data := buildForYou(ctx, refresher.service, userID)
	if data.HeroMovie == nil {
		return fmt.Errorf("no recommendation data for user %d", userID)
	}
	return refresher.store.Save(ctx, userID, data)
}

func (refresher *Refresher) Handle(ctx context.Context, job workqueue.Job) error {
	userID, err := strconv.Atoi(job.SubjectKey)
	if err != nil || userID <= 0 {
		return workqueue.Terminal(fmt.Errorf("invalid recommendation user %q", job.SubjectKey))
	}
	return refresher.Refresh(ctx, userID)
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%f seconds", duration.Seconds())
}
