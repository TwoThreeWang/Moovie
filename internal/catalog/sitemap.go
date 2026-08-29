package catalog

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/content"
)

// SitemapProvider 给 SEO 站点地图提供影片列表。
type SitemapProvider struct{ store Store }

// NewSitemapProvider 创建站点地图数据源。
func NewSitemapProvider(store Store) SitemapProvider { return SitemapProvider{store: store} }

func (provider SitemapProvider) CountForSitemap(ctx context.Context, kind content.SitemapKind) (int, error) {
	if optimized, ok := provider.store.(interface {
		CountForSitemap(context.Context, content.SitemapKind) (int, error)
	}); ok {
		return optimized.CountForSitemap(ctx, kind)
	}
	return 0, fmt.Errorf("catalog store does not support sitemap counts")
}

func (provider SitemapProvider) PageForSitemap(ctx context.Context, kind content.SitemapKind, limit, offset int) ([]content.SitemapMovie, error) {
	if optimized, ok := provider.store.(interface {
		PageForSitemap(context.Context, content.SitemapKind, int, int) ([]content.SitemapMovie, error)
	}); ok {
		return optimized.PageForSitemap(ctx, kind, limit, offset)
	}
	return nil, fmt.Errorf("catalog store does not support sitemap pages")
}
