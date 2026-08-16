package history

import (
	"context"
	"time"
)

type Store interface {
	ListByUser(ctx context.Context, userID, limit, offset int) ([]Record, error)
	ListContinue(ctx context.Context, userID, limit, offset int) ([]Record, error)
	CountByUser(ctx context.Context, userID int) (int, error)
	SyncV2(ctx context.Context, userID int, request SyncV2Request, receivedAt time.Time) (SyncV2Result, error)
}
