package service

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
)

// SearchService 搜索服务
type SearchService struct {
	siteRepo   *repository.SiteRepository
	cacheRepo  *repository.SearchCacheRepository
	crawler    SourceCrawler
	cacheTTL   time.Duration // 缓存有效期
	maxTimeout time.Duration // 单站点最大超时时间
}

// NewSearchService 创建搜索服务
func NewSearchService(
	siteRepo *repository.SiteRepository,
	cacheRepo *repository.SearchCacheRepository,
	crawler SourceCrawler,
) *SearchService {
	return &SearchService{
		siteRepo:   siteRepo,
		cacheRepo:  cacheRepo,
		crawler:    crawler,
		cacheTTL:   24 * time.Hour, // 缓存1天
		maxTimeout: 10 * time.Second,
	}
}

// SearchResult 搜索结果
type SearchResult struct {
	Items     []model.VodItem `json:"items"`
	FromCache bool            `json:"from_cache"`
	Expired   bool            `json:"expired"`
}

// Search 搜索视频
// 1. 有缓存且<1天: 直接返回
// 2. 有缓存但>=1天: 先返回旧数据，异步刷新
// 3. 无缓存: 同步获取并缓存
func (s *SearchService) Search(ctx context.Context, keyword string) (*SearchResult, error) {
	// 查询缓存
	cache, isExpired, err := s.cacheRepo.FindWithExpiry(keyword)
	if err != nil {
		log.Printf("[SearchService] 查询缓存失败: %v", err)
	}

	// 有缓存
	if cache != nil {
		var items []model.VodItem
		if err := json.Unmarshal([]byte(cache.ResultJSON), &items); err != nil {
			log.Printf("[SearchService] 解析缓存JSON失败: %v", err)
		} else {
			// 缓存未过期，直接返回
			if !isExpired {
				return &SearchResult{
					Items:     items,
					FromCache: true,
					Expired:   false,
				}, nil
			}

			// 缓存已过期，先返回旧数据，异步刷新
			go s.refreshCache(keyword)
			return &SearchResult{
				Items:     items,
				FromCache: true,
				Expired:   true,
			}, nil
		}
	}

	// 无缓存，同步获取
	items, err := s.fetchFromSources(ctx, keyword)
	if err != nil {
		return nil, err
	}

	// 保存缓存
	go s.saveCache(keyword, items)

	return &SearchResult{
		Items:     items,
		FromCache: false,
		Expired:   false,
	}, nil
}

// GetDetail 获取视频详情
func (s *SearchService) GetDetail(ctx context.Context, sourceKey, vodId string) (*model.VodItem, error) {
	// 查找站点
	site, err := s.siteRepo.FindByKey(sourceKey)
	if err != nil {
		return nil, err
	}
	if site == nil {
		return nil, nil
	}

	// 获取详情
	return s.crawler.GetDetail(ctx, site.BaseUrl, vodId, sourceKey)
}

// fetchFromSources 从所有启用的资源网并发获取数据
func (s *SearchService) fetchFromSources(ctx context.Context, keyword string) ([]model.VodItem, error) {
	// 获取所有启用的站点
	sites, err := s.siteRepo.ListEnabled()
	if err != nil {
		return nil, err
	}

	if len(sites) == 0 {
		return []model.VodItem{}, nil
	}

	// 并发获取
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allItems []model.VodItem

	for _, site := range sites {
		wg.Add(1)
		go func(site *model.Site) {
			defer wg.Done()

			// 创建带超时的上下文
			reqCtx, cancel := context.WithTimeout(ctx, s.maxTimeout)
			defer cancel()

			items, err := s.crawler.Search(reqCtx, site.BaseUrl, keyword, site.Key)
			if err != nil {
				log.Printf("[SearchService] 站点 %s 搜索失败: %v", site.Key, err)
				return
			}

			mu.Lock()
			allItems = append(allItems, items...)
			mu.Unlock()

			log.Printf("[SearchService] 站点 %s 返回 %d 条结果", site.Key, len(items))
		}(site)
	}

	wg.Wait()
	return allItems, nil
}

// refreshCache 异步刷新缓存
func (s *SearchService) refreshCache(keyword string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := s.fetchFromSources(ctx, keyword)
	if err != nil {
		log.Printf("[SearchService] 异步刷新缓存失败: %v", err)
		return
	}

	s.saveCache(keyword, items)
	log.Printf("[SearchService] 异步刷新缓存成功: %s", keyword)
}

// saveCache 保存缓存
func (s *SearchService) saveCache(keyword string, items []model.VodItem) {
	jsonData, err := json.Marshal(items)
	if err != nil {
		log.Printf("[SearchService] 序列化缓存失败: %v", err)
		return
	}

	if err := s.cacheRepo.Upsert(keyword, string(jsonData), s.cacheTTL); err != nil {
		log.Printf("[SearchService] 保存缓存失败: %v", err)
	}
}
