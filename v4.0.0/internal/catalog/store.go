package catalog

import "context"

type Store interface {
	FindByDoubanID(ctx context.Context, doubanID string) (*Movie, error)
	FindByID(ctx context.Context, id int) (*Movie, error)
	FindSimilar(ctx context.Context, doubanID string, limit int) ([]Movie, error)
	Upsert(ctx context.Context, movie Movie) error
	DeleteByDoubanID(ctx context.Context, doubanID string) error
	Latest(ctx context.Context, limit int) ([]Movie, error)
	Suggest(ctx context.Context, keyword string, limit int) ([]Movie, error)
	Popular(ctx context.Context, limit int) ([]Movie, error)
	UpdateEmbedding(ctx context.Context, doubanID, content, semanticHash string, embedding []float32) error
	Count(ctx context.Context) (int, error)
}
