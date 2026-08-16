package feedback

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type PostgresStore struct{ database database.Executor }

func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

const feedbackColumns = `id, user_id, type, content, movie_url, status, reply, replied_at, created_at`

func (store *PostgresStore) Create(ctx context.Context, record Feedback) (*Feedback, error) {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	record.Status = StatusPending
	if err := store.database.QueryRow(ctx, `INSERT INTO feedbacks
(user_id, type, content, movie_url, status, reply, replied_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, record.UserID, record.Type, record.Content,
		record.MovieURL, record.Status, record.Reply, record.RepliedAt, record.CreatedAt).Scan(&record.ID); err != nil {
		return nil, fmt.Errorf("create feedback: %w", err)
	}
	return &record, nil
}

func (store *PostgresStore) ListPublic(ctx context.Context, feedbackType string, limit, offset int) ([]Feedback, error) {
	if feedbackType == "" {
		return store.list(ctx, `SELECT `+feedbackColumns+` FROM feedbacks ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	return store.list(ctx, `SELECT `+feedbackColumns+` FROM feedbacks WHERE type = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, feedbackType, limit, offset)
}

func (store *PostgresStore) CountPublic(ctx context.Context, feedbackType string) (int, error) {
	if feedbackType == "" {
		return store.count(ctx, `SELECT COUNT(*) FROM feedbacks`)
	}
	return store.count(ctx, `SELECT COUNT(*) FROM feedbacks WHERE type = $1`, feedbackType)
}

func (store *PostgresStore) ListByUser(ctx context.Context, userID, limit, offset int) ([]Feedback, error) {
	return store.list(ctx, `SELECT `+feedbackColumns+` FROM feedbacks WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
}

func (store *PostgresStore) CountByUser(ctx context.Context, userID int) (int, error) {
	return store.count(ctx, `SELECT COUNT(*) FROM feedbacks WHERE user_id = $1`, userID)
}

func (store *PostgresStore) ListAdmin(ctx context.Context, status string, limit, offset int) ([]Feedback, error) {
	if status == "" {
		return store.list(ctx, `SELECT `+feedbackColumns+` FROM feedbacks ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	return store.list(ctx, `SELECT `+feedbackColumns+` FROM feedbacks WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
}

func (store *PostgresStore) CountPending(ctx context.Context) (int, error) {
	return store.count(ctx, `SELECT COUNT(*) FROM feedbacks WHERE status = 'pending'`)
}

func (store *PostgresStore) UpdateStatus(ctx context.Context, id int, status string) error {
	if _, err := store.database.Exec(ctx, `UPDATE feedbacks SET status = $2 WHERE id = $1`, id, status); err != nil {
		return fmt.Errorf("update feedback status: %w", err)
	}
	return nil
}

func (store *PostgresStore) Reply(ctx context.Context, id int, reply string) error {
	if _, err := store.database.Exec(ctx, `UPDATE feedbacks SET status = 'resolved', reply = $2, replied_at = NOW() WHERE id = $1`, id, reply); err != nil {
		return fmt.Errorf("reply to feedback: %w", err)
	}
	return nil
}

func (store *PostgresStore) list(ctx context.Context, query string, arguments ...any) ([]Feedback, error) {
	rows, err := store.database.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list feedback: %w", err)
	}
	defer rows.Close()
	records := make([]Feedback, 0)
	for rows.Next() {
		var record Feedback
		if err := rows.Scan(&record.ID, &record.UserID, &record.Type, &record.Content, &record.MovieURL,
			&record.Status, &record.Reply, &record.RepliedAt, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feedback: %w", err)
	}
	return records, nil
}

func (store *PostgresStore) count(ctx context.Context, query string, arguments ...any) (int, error) {
	var count int
	if err := store.database.QueryRow(ctx, query, arguments...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count feedback: %w", err)
	}
	return count, nil
}
