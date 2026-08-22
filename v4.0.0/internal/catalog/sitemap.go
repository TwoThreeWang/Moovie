package catalog

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/content"
)

// SitemapProvider 给 SEO 站点地图提供影片列表。
type SitemapProvider struct{ store Store }

// NewSitemapProvider 创建站点地图数据源。
func NewSitemapProvider(store Store) SitemapProvider { return SitemapProvider{store: store} }

// LatestForSitemap 优先用只查两列的轻量实现；存储层不支持时才退回加载完整影片。
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
