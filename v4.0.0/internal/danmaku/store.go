package danmaku

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRateLimited = errors.New("danmaku rate limited")
	ErrDuplicate   = errors.New("duplicate danmaku")
)

type Store interface {
	ListByVodKey(ctx context.Context, vodKey string, limit int) ([]Record, error)
	CreateGuarded(ctx context.Context, record Record, rateSince, duplicateSince time.Time, maxPerWindow int) (*Record, error)
	SoftDelete(ctx context.Context, id int) error
	ListRecent(ctx context.Context, limit, offset int) ([]Record, error)
}
