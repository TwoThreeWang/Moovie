package danmaku

import (
	"context"
	"errors"
	"time"
)

// 发送弹幕被拒的两种原因：太频繁、内容重复。
var (
	ErrRateLimited = errors.New("danmaku rate limited")
	ErrDuplicate   = errors.New("duplicate danmaku")
)

// Store 是弹幕的读写接口。
type Store interface {
	ListByVodKey(ctx context.Context, vodKey string, limit int) ([]Record, error)
	CreateGuarded(ctx context.Context, record Record, rateSince, duplicateSince time.Time, maxPerWindow int) (*Record, error)
	SoftDelete(ctx context.Context, id int) error
	ListRecent(ctx context.Context, limit, offset int) ([]Record, error)
}
