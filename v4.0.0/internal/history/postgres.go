package history

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type PostgresStore struct {
	database database.Executor
	beginner database.Beginner
}

type PostgresOption func(*PostgresStore)

func NewPostgresStore(executor database.Executor, options ...PostgresOption) *PostgresStore {
	store := &PostgresStore{database: executor}
	if beginner, ok := executor.(database.Beginner); ok {
		store.beginner = beginner
	}
	for _, option := range options {
		option(store)
	}
	return store
}

func (store *PostgresStore) Upsert(ctx context.Context, record Record) error {
	return upsertRecord(ctx, store.database, record)
}

func upsertRecord(ctx context.Context, executor database.Executor, record Record) error {
	operationType := "upsert"
	if record.Progress >= 100 {
		operationType = "complete"
	}
	return upsertPlaybackPosition(ctx, executor, record.UserID, SyncOperation{
		Type: operationType, HistoryID: record.ID, MediaID: record.MediaID, MediaUnitID: record.MediaUnitID,
		DoubanID: record.DoubanID, Source: record.Source, VodID: record.VodID, Title: record.Title,
		Poster: record.Poster, Episode: record.Episode, Season: record.SeasonNumber, EpisodeKey: record.EpisodeKey,
		Position: record.LastTime, Duration: record.Duration, Progress: record.Progress,
		EntryPage:  record.EntryPage,
		OccurredAt: recordTime(record),
	})
}

func (store *PostgresStore) ListByUser(ctx context.Context, userID, limit, offset int) ([]Record, error) {
	return queryPlaybackPositions(ctx, store.database,
		`position.user_id = $1 AND position.deleted_at IS NULL
ORDER BY position.activity_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
}

// CountByUser 统计「在看」数量，口径与 ListContinue 一致：排除已看完的，
// 也排除已经在片单里标记为看过的。仪表盘的「部在看」用的就是这个数字，
// 如果只按 deleted_at 过滤，会把看完的也算进去，和下方列表对不上。
func (store *PostgresStore) CountByUser(ctx context.Context, userID int) (int, error) {
	var count int64
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM playback_positions position
WHERE position.user_id = $1 AND position.deleted_at IS NULL AND position.completed = FALSE
AND NOT EXISTS (
    SELECT 1 FROM user_movies
    WHERE user_movies.user_id = position.user_id
      AND user_movies.media_id IS NOT NULL
      AND user_movies.media_id = position.media_id
      AND user_movies.status = 'watched'
      AND user_movies.updated_at >= position.activity_at
)`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count playback positions: %w", err)
	}
	return int(count), nil
}
