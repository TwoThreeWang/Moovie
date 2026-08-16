package library

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

// movie_id 继续保存公开 URL 使用的豆瓣标识；media_id 是数据库内部规范外键。
const recordColumns = `um.id, um.user_id, um.movie_id,
COALESCE(NULLIF(media.title, ''), um.title),
COALESCE(NULLIF(media.poster, ''), um.poster),
COALESCE(NULLIF(media.year, ''), um.year),
um.status, um.rating, um.comment, um.created_at, um.updated_at`

const recordSource = ` FROM user_movies um LEFT JOIN media ON media.id = um.media_id`

func (store *PostgresStore) Upsert(ctx context.Context, record Record) error {
	hasExternalTime := !record.CreatedAt.IsZero() || !record.UpdatedAt.IsZero()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now()
	}
	_, err := store.database.Exec(ctx, `INSERT INTO user_movies
(user_id, media_id, movie_id, title, poster, year, status, rating, comment, created_at, updated_at)
VALUES ($1,(SELECT id FROM media WHERE douban_id=$2 LIMIT 1),$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (user_id, movie_id) DO UPDATE SET
media_id = COALESCE(EXCLUDED.media_id, user_movies.media_id),
title = EXCLUDED.title, poster = EXCLUDED.poster, year = EXCLUDED.year,
status = EXCLUDED.status, rating = EXCLUDED.rating, comment = EXCLUDED.comment,
updated_at = EXCLUDED.updated_at,
created_at = CASE WHEN $11 THEN EXCLUDED.created_at ELSE user_movies.created_at END`, record.UserID, record.MovieID, record.Title, record.Poster, record.Year,
		record.Status, record.Rating, record.Comment, record.CreatedAt, record.UpdatedAt, hasExternalTime)
	if err != nil {
		return fmt.Errorf("upsert user movie: %w", err)
	}
	return nil
}

func (store *PostgresStore) ListByUserAndDateRange(ctx context.Context, userID int, status string, start, end time.Time) ([]Record, error) {
	rows, err := store.database.Query(ctx, `SELECT `+recordColumns+recordSource+`
WHERE um.user_id = $1 AND um.status = $2 AND um.created_at >= $3 AND um.created_at < $4 ORDER BY um.updated_at DESC`, userID, status, start, end)
	if err != nil {
		return nil, fmt.Errorf("list user movies by date: %w", err)
	}
	defer rows.Close()
	return scanRecords(rows)
}

func (store *PostgresStore) CountWatchedByAllUsersInRange(ctx context.Context, start, end time.Time) (map[int]int, error) {
	rows, err := store.database.Query(ctx, `SELECT user_id, COUNT(*) FROM user_movies
WHERE status = 'watched' AND created_at >= $1 AND created_at < $2 GROUP BY user_id`, start, end)
	if err != nil {
		return nil, fmt.Errorf("count monthly watched movies: %w", err)
	}
	defer rows.Close()
	counts := make(map[int]int)
	for rows.Next() {
		var userID, count int
		if err := rows.Scan(&userID, &count); err != nil {
			return nil, fmt.Errorf("scan monthly watched count: %w", err)
		}
		counts[userID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monthly watched counts: %w", err)
	}
	return counts, nil
}

func (store *PostgresStore) AvgRatingByUser(ctx context.Context, userID int) (float64, int, error) {
	var average float64
	var count int
	if err := store.database.QueryRow(ctx, `SELECT COALESCE(AVG(rating), 0), COUNT(*) FROM user_movies
WHERE user_id = $1 AND status = 'watched' AND rating > 0`, userID).Scan(&average, &count); err != nil {
		return 0, 0, fmt.Errorf("average user rating: %w", err)
	}
	return average, count, nil
}

func (store *PostgresStore) CountOverlapWatched(ctx context.Context, userA, userB int) (int, error) {
	if userA == userB {
		return 0, nil
	}
	var count int
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM user_movies a
JOIN user_movies b ON a.movie_id = b.movie_id AND b.user_id = $2 AND b.status = 'watched'
WHERE a.user_id = $1 AND a.status = 'watched'`, userA, userB).Scan(&count); err != nil {
		return 0, fmt.Errorf("count watched overlap: %w", err)
	}
	return count, nil
}

func (store *PostgresStore) CountByUserAndDateRange(ctx context.Context, userID int, status string, start, end time.Time) (int, error) {
	var count int
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM user_movies
WHERE user_id = $1 AND status = $2 AND created_at >= $3 AND created_at < $4`, userID, status, start, end).Scan(&count); err != nil {
		return 0, fmt.Errorf("count user movies by date: %w", err)
	}
	return count, nil
}

func scanRecords(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Record, error) {
	records := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.UserID, &record.MovieID, &record.Title, &record.Poster,
			&record.Year, &record.Status, &record.Rating, &record.Comment, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user movie: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user movies: %w", err)
	}
	return records, nil
}

func (store *PostgresStore) Remove(ctx context.Context, userID int, movieID string) error {
	if _, err := store.database.Exec(ctx, `DELETE FROM user_movies WHERE user_id = $1 AND movie_id = $2`, userID, movieID); err != nil {
		return fmt.Errorf("remove user movie: %w", err)
	}
	return nil
}

func (store *PostgresStore) GetByUserAndMovie(ctx context.Context, userID int, movieID string) (*Record, error) {
	return store.find(ctx, `SELECT `+recordColumns+recordSource+` WHERE um.user_id = $1 AND um.movie_id = $2 LIMIT 1`, userID, movieID)
}

func (store *PostgresStore) GetByID(ctx context.Context, userID, id int) (*Record, error) {
	return store.find(ctx, `SELECT `+recordColumns+recordSource+` WHERE um.user_id = $1 AND um.id = $2 LIMIT 1`, userID, id)
}

func (store *PostgresStore) IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error) {
	var marked bool
	if err := store.database.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM user_movies WHERE user_id = $1 AND movie_id = $2 AND status = $3
)`, userID, movieID, status).Scan(&marked); err != nil {
		return false, fmt.Errorf("check user movie: %w", err)
	}
	return marked, nil
}

func (store *PostgresStore) find(ctx context.Context, query string, arguments ...any) (*Record, error) {
	rows, err := store.database.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("find user movie: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var record Record
	if err := rows.Scan(&record.ID, &record.UserID, &record.MovieID, &record.Title, &record.Poster,
		&record.Year, &record.Status, &record.Rating, &record.Comment, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scan user movie: %w", err)
	}
	return &record, nil
}

func (store *PostgresStore) ListByUser(ctx context.Context, userID int, status string, limit, offset int) ([]Record, error) {
	rows, err := store.database.Query(ctx, `SELECT `+recordColumns+recordSource+`
WHERE um.user_id = $1 AND um.status = $2 ORDER BY um.updated_at DESC LIMIT $3 OFFSET $4`, userID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list user movies: %w", err)
	}
	defer rows.Close()
	records := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.UserID, &record.MovieID, &record.Title, &record.Poster,
			&record.Year, &record.Status, &record.Rating, &record.Comment, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user movie: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user movies: %w", err)
	}
	return records, nil
}

func (store *PostgresStore) CountByUser(ctx context.Context, userID int, status string) (int, error) {
	var count int64
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM user_movies WHERE user_id = $1 AND status = $2`, userID, status).Scan(&count); err != nil {
		return 0, fmt.Errorf("count user movies: %w", err)
	}
	return int(count), nil
}

func (store *PostgresStore) CountByMovie(ctx context.Context, movieID, status string) (int, error) {
	var count int64
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM user_movies WHERE movie_id = $1 AND status = $2`, movieID, status).Scan(&count); err != nil {
		return 0, fmt.Errorf("count movie marks: %w", err)
	}
	return int(count), nil
}

func (store *PostgresStore) UpdateRatingComment(ctx context.Context, userID, id, rating int, comment string) error {
	if _, err := store.database.Exec(ctx, `UPDATE user_movies SET rating = $3, comment = $4, updated_at = NOW()
WHERE user_id = $1 AND id = $2`, userID, id, rating, comment); err != nil {
		return fmt.Errorf("update user movie: %w", err)
	}
	return nil
}
