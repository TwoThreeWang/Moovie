package collection

import "context"

// Store 是片单的读写接口。
type Store interface {
	// 公开页
	ListFeatured(ctx context.Context, limit, offset int) ([]Collection, error)
	CountFeatured(ctx context.Context) (int, error)
	GetBySlug(ctx context.Context, slug string) (*Collection, error)
	ListRelated(ctx context.Context, collectionID int, publicOnly bool) ([]RelatedCollection, error)
	ListItems(ctx context.Context, collectionID int) ([]Item, error)
	FeaturedForSitemap(ctx context.Context) ([]Collection, error)

	// 后台
	ListAll(ctx context.Context, limit, offset int) ([]Collection, error)
	Save(ctx context.Context, collection Collection, items []ItemInput) (id int, unknown []string, err error)
	Delete(ctx context.Context, id int) error
}
