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

func (store *PostgresStore) CountByUser(ctx context.Context, userID int) (int, error) {
	var count int64
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM playback_positions
WHERE user_id = $1 AND deleted_at IS NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count playback positions: %w", err)
	}
	return int(count), nil
}
