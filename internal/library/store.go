package library

import (
	"context"
	"time"
)

// Store 是片单的读写接口。后半部分的统计方法主要给月报和片场用。
type Store interface {
	Upsert(ctx context.Context, record Record) error
	Remove(ctx context.Context, userID int, movieID string) error
	GetByUserAndMovie(ctx context.Context, userID int, movieID string) (*Record, error)
	GetByID(ctx context.Context, userID, id int) (*Record, error)
	IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error)
	ListByUser(ctx context.Context, userID int, status string, limit, offset int) ([]Record, error)
	CountByUser(ctx context.Context, userID int, status string) (int, error)
	CountByMovie(ctx context.Context, movieID, status string) (int, error)
	UpdateRatingComment(ctx context.Context, userID, id, rating int, comment string) error
	ListByUserAndDateRange(ctx context.Context, userID int, status string, start, end time.Time) ([]Record, error)
	CountWatchedByAllUsersInRange(ctx context.Context, start, end time.Time) (map[int]int, error)
	AvgRatingByUser(ctx context.Context, userID int) (float64, int, error)
	CountOverlapWatched(ctx context.Context, userA, userB int) (int, error)
	CountByUserAndDateRange(ctx context.Context, userID int, status string, start, end time.Time) (int, error)
}
