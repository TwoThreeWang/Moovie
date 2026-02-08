package service

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"golang.org/x/sync/singleflight"
)

// SearchService 搜索服务
type SearchService struct {
	siteRepo      *repository.SiteRepository
	vodItemRepo   *repository.VodItemRepository
	copyrightRepo *repository.CopyrightFilterRepository
	categoryRepo  *repository.CategoryFilterRepository
	crawler       SourceCrawler
	maxTimeout    time.Duration // 单站点最大超时时间
	sf            singleflight.Group
}

// NewSearchService 创建搜索服务
func NewSearchService(
	siteRepo *repository.SiteRepository,
	vodItemRepo *repository.VodItemRepository,
	copyrightRepo *repository.CopyrightFilterRepository,
	categoryRepo *repository.CategoryFilterRepository,
	crawler SourceCrawler,
) *SearchService {
	return &SearchService{
		siteRepo:      siteRepo,
		vodItemRepo:   vodItemRepo,
		copyrightRepo: copyrightRepo,
		categoryRepo:  categoryRepo,
		crawler:       crawler,
		maxTimeout:    10 * time.Second,
		sf:            singleflight.Group{},
	}
}

// GetSearchCrawler 获取搜素爬虫
func (s *SearchService) GetSearchCrawler() SourceCrawler {
	return s.crawler
}

// SearchResult 搜索结果
type SearchResult struct {
	Items         []model.VodItem `json:"items"`
	FilteredCount int             `json:"filtered_count"` // 版权过滤数量
}

// Search 搜索视频
// 1. 先从本地数据库搜索
// 2. 如果本地为空，则同步从资源网获取并刷新（带超时）
// 3. 如果本地不为空，则异步刷新数据
// 4. 过滤版权限制内容
func (s *SearchService) Search(ctx context.Context, keyword string, bypassFilter bool) (*SearchResult, error) {
	// 1. 从本地数据库搜索
	items, err := s.vodItemRepo.Search(keyword)
	if err != nil {
		log.Printf("[SearchService] 本地搜索失败: %v", err)
	}

	// 2. 如果本地没有结果，同步等待
	if len(items) == 0 {
		log.Printf("[SearchService] 本地结果为空，尝试同步刷新: %s", keyword)
		// 使用 singleflight 避免并发请求同一个词
		val, err, _ := s.sf.Do(keyword, func() (interface{}, error) {
			return s.fetchAndSave(ctx, keyword)
		})

		if err != nil {
			log.Printf("[SearchService] 同步刷新失败: %v", err)
		} else if val != nil {
			items = val.([]model.VodItem)
		}
	} else {
		// 3. 本地有结果，异步刷新
		go func() {
			_, _, _ = s.sf.Do(keyword, func() (interface{}, error) {
				return s.fetchAndSave(context.Background(), keyword)
			})
		}()
	}

	// 4. 版权过滤（可通过参数跳过）
	var filteredCount int
	if !bypassFilter {
		items, filteredCount = s.filterCopyrightContent(items)
	}

	// 5. 排序处理 (默认按加载速度排序)
	sort.Slice(items, func(i, j int) bool {
		// 如果两个都没有速度数据，保持原序
		if items[i].AvgSpeedMs == 0 && items[j].AvgSpeedMs == 0 {
			return false
		}
		// 有速度数据的排前面
		if items[i].AvgSpeedMs > 0 && items[j].AvgSpeedMs == 0 {
			return true
		}
		if items[i].AvgSpeedMs == 0 && items[j].AvgSpeedMs > 0 {
			return false
		}
		// 都有速度数据的，耗时少的排前面
		return items[i].AvgSpeedMs < items[j].AvgSpeedMs
	})

	return &SearchResult{
		Items:         items,
		FilteredCount: filteredCount,
	}, nil
}

// GetDetail 获取视频详情
func (s *SearchService) GetDetail(ctx context.Context, sourceKey, vodId string) (*model.VodItem, error) {
	// 1. 先尝试从数据库获取
	item, err := s.vodItemRepo.FindBySourceId(sourceKey, vodId)
	if err == nil && item != nil {
		// 如果数据库里有，直接返回，但异步触发一次刷新，保证播放链接是最新的
		go func() {
			key := "detail:" + sourceKey + ":" + vodId
			_, _, _ = s.sf.Do(key, func() (interface{}, error) {
				return s.fetchAndSaveDetail(context.Background(), sourceKey, vodId)
			})
		}()
		return item, nil
	}

	// 2. 数据库没有，使用 singleflight 同步获取并保存
	key := "detail:" + sourceKey + ":" + vodId
	val, err, _ := s.sf.Do(key, func() (interface{}, error) {
		return s.fetchAndSaveDetail(ctx, sourceKey, vodId)
	})

	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	return val.(*model.VodItem), nil
}

// fetchAndSaveDetail 从爬虫获取详情并保存到数据库
func (s *SearchService) fetchAndSaveDetail(ctx context.Context, sourceKey, vodId string) (*model.VodItem, error) {
	// 1. 查找站点配置
	site, err := s.siteRepo.FindByKey(sourceKey)
	if err != nil {
		return nil, err
	}
	if site == nil {
		return nil, nil
	}

	// 2. 从爬虫获取详情
	detail, err := s.crawler.GetDetail(ctx, site.BaseUrl, vodId, sourceKey)
	if err != nil {
		return nil, err
	}

	if detail != nil {
		// 3. 保存到数据库
		if err := s.vodItemRepo.Upsert(detail); err != nil {
			log.Printf("[SearchService] 保存视频详情到数据库失败: %v", err)
		}
	}

	return detail, nil
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

			// 获取分类过滤关键词
			categories, _ := s.categoryRepo.GetAllKeywords()

			items, err := s.crawler.Search(reqCtx, site.BaseUrl, keyword, site.Key, categories)
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

// fetchAndSave 从资源网获取并更新数据库，返回结果供同步调用
func (s *SearchService) fetchAndSave(ctx context.Context, keyword string) ([]model.VodItem, error) {
	// 增加总超时控制，防止资源网过慢
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	items, err := s.fetchFromSources(ctx, keyword)
	if err != nil {
		return nil, err
	}

	if len(items) > 0 {
		for _, item := range items {
			if err := s.vodItemRepo.Upsert(&item); err != nil {
				log.Printf("[SearchService] 更新视频数据失败: %v", err)
			}
		}
		log.Printf("[SearchService] 刷新数据成功: %s, 共 %d 条", keyword, len(items))
	}

	return items, nil
}

// filterCopyrightContent 过滤版权限制内容，返回过滤后的列表和被过滤的数量
func (s *SearchService) filterCopyrightContent(items []model.VodItem) ([]model.VodItem, int) {
	// 获取版权关键词列表
	keywords, err := s.copyrightRepo.GetAllKeywords()
	if err != nil || len(keywords) == 0 {
		return items, 0
	}

	// 过滤包含版权关键词的内容
	filtered := make([]model.VodItem, 0, len(items))
	for _, item := range items {
		blocked := false
		for _, kw := range keywords {
			if strings.Contains(strings.ToLower(item.VodName), strings.ToLower(kw)) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, item)
		}
	}

	filteredCount := len(items) - len(filtered)
	if filteredCount > 0 {
		log.Printf("[SearchService] 版权过滤: 原 %d 条，过滤后 %d 条", len(items), len(filtered))
	}

	return filtered, filteredCount
}
