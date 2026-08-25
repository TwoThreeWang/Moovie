package playback

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"golang.org/x/sync/singleflight"
)

// DetailService 取资源详情：库里有就直接返回并在后台顺手刷新一次，
// 库里没有才同步去资源站抓。singleflight 保证同一条资源同时只抓一次。
type DetailService struct {
	catalog Catalog
	sites   SiteCatalog
	crawler DetailCrawler
	runner  BackgroundRunner
	timeout time.Duration
	group   singleflight.Group
}

// NewDetailService 创建详情服务。
func NewDetailService(catalog Catalog, sites SiteCatalog, crawler DetailCrawler, runner BackgroundRunner, timeout time.Duration) *DetailService {
	return &DetailService{catalog: catalog, sites: sites, crawler: crawler, runner: runner, timeout: timeout}
}

// Get 优先读库，读不到才回源抓取。
func (service *DetailService) Get(ctx context.Context, sourceKey, vodID string) (*search.VodItem, error) {
	item, err := service.catalog.FindBySourceID(ctx, sourceKey, vodID)
	if err == nil && item != nil {
		if service.runner != nil {
			service.runner.Run(func(taskContext context.Context) {
				_, _ = service.fetch(taskContext, sourceKey, vodID)
			})
		}
		return item, nil
	}
	return service.fetch(ctx, sourceKey, vodID)
}

// Refresh 强制回源抓取，忽略库里已有的数据。
func (service *DetailService) Refresh(ctx context.Context, sourceKey, vodID string) (*search.VodItem, error) {
	return service.fetch(ctx, sourceKey, vodID)
}

// fetch 回源抓详情并写库。
func (service *DetailService) fetch(ctx context.Context, sourceKey, vodID string) (*search.VodItem, error) {
	key := "detail:" + sourceKey + ":" + vodID
	value, err, _ := service.group.Do(key, func() (any, error) {
		site, err := service.sites.FindSiteByKey(ctx, sourceKey)
		if err != nil {
			return nil, fmt.Errorf("find source: %w", err)
		}
		if site == nil {
			return nil, nil
		}
		requestContext := ctx
		cancel := func() {}
		if service.timeout > 0 {
			requestContext, cancel = context.WithTimeout(ctx, service.timeout)
		}
		defer cancel()
		item, err := service.crawler.GetDetail(requestContext, site.BaseURL, vodID, sourceKey)
		if err != nil {
			return nil, err
		}
		if item != nil {
			if err := service.catalog.Upsert(ctx, *item); err != nil {
				return nil, fmt.Errorf("save detail: %w", err)
			}
		}
		return item, nil
	})
	if err != nil || value == nil {
		return nil, err
	}
	return value.(*search.VodItem), nil
}
