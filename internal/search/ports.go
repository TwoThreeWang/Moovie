package search

import "context"

// ItemStore 是资源条目的读写口子（对应 vod_items 表）。
type ItemStore interface {
	Search(ctx context.Context, keyword string) ([]VodItem, error)
	Upsert(ctx context.Context, item VodItem) error
}

// SiteStore 提供启用中的资源站清单。
type SiteStore interface {
	ListEnabled(ctx context.Context) ([]Site, error)
}

// FilterStore 提供版权屏蔽词和分类屏蔽词。
type FilterStore interface {
	CopyrightKeywords(ctx context.Context) ([]string, error)
	CategoryKeywords(ctx context.Context) ([]string, error)
}

// SourceCrawler 是对单个资源站发起搜索的抓取器（实现见 applecms.go）。
type SourceCrawler interface {
	Search(ctx context.Context, baseURL, keyword, sourceKey string, restrictedCategories []string) ([]VodItem, error)
}

// HealthMonitor 负责熔断：过滤掉连续失败的资源站，并记录每次抓取结果。
type HealthMonitor interface {
	FilterAvailable(sites []Site) (available []Site, skipped []string)
	Record(siteKey string, outcome Outcome, elapsedMilliseconds int64)
}

// BackgroundRunner 是有并发上限的后台任务执行器，避免请求线程被异步任务拖住。
type BackgroundRunner interface {
	Run(task func(context.Context))
}

// SearchLogStore 记录搜索日志并统计热搜。
type SearchLogStore interface {
	Log(ctx context.Context, keyword string, userID *int, ipHash string) error
	Trending(ctx context.Context, hours, limit int) ([]TrendingKeyword, error)
}

// HealthStatStore 批量落库资源站健康统计。
type HealthStatStore interface {
	AddHealthStats(ctx context.Context, stats []HealthStat) error
}
