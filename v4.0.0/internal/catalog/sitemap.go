package catalog

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/content"
)

type SitemapProvider struct{ store Store }

func NewSitemapProvider(store Store) SitemapProvider { return SitemapProvider{store: store} }

func (provider SitemapProvider) LatestForSitemap(ctx context.Context, limit int) ([]content.SitemapMovie, error) {
	if optimized, ok := provider.store.(interface {
		LatestForSitemap(context.Context, int) ([]content.SitemapMovie, error)
	}); ok {
		return optimized.LatestForSitemap(ctx, limit)
	}
	movies, err := provider.store.Latest(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]content.SitemapMovie, 0, len(movies))
	for _, movie := range movies {
		result = append(result, content.SitemapMovie{DoubanID: movie.DoubanID, UpdatedAt: movie.UpdatedAt})
	}
	return result, nil
}
