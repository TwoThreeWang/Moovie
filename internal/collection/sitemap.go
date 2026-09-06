package collection

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/content"
)

// SitemapProvider 把片单接到 SEO 站点地图上。
// 片单页是拉新资产，不进 sitemap 等于白做——但只收录已发布且非空的片单，
// 薄内容页进索引会拖累整站权重。
type SitemapProvider struct{ store Store }

// NewSitemapProvider 创建片单站点地图数据源。
func NewSitemapProvider(store Store) SitemapProvider { return SitemapProvider{store: store} }

// SlugsForSitemap 返回要收录的片单地址和更新时间。
func (provider SitemapProvider) SlugsForSitemap(ctx context.Context) ([]content.SitemapCollection, error) {
	collections, err := provider.store.FeaturedForSitemap(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]content.SitemapCollection, 0, len(collections))
	for _, item := range collections {
		entries = append(entries, content.SitemapCollection{Slug: item.Slug, UpdatedAt: item.UpdatedAt})
	}
	return entries, nil
}
