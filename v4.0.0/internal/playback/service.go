package playback

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"golang.org/x/sync/singleflight"
)

type DetailService struct {
	catalog Catalog
	sites   SiteCatalog
	crawler DetailCrawler
	runner  BackgroundRunner
	timeout time.Duration
	group   singleflight.Group
}

func NewDetailService(catalog Catalog, sites SiteCatalog, crawler DetailCrawler, runner BackgroundRunner, timeout time.Duration) *DetailService {
	return &DetailService{catalog: catalog, sites: sites, crawler: crawler, runner: runner, timeout: timeout}
}

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

func (service *DetailService) Refresh(ctx context.Context, sourceKey, vodID string) (*search.VodItem, error) {
	return service.fetch(ctx, sourceKey, vodID)
}

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
