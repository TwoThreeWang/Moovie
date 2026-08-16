package search

import "context"

type ItemStore interface {
	Search(ctx context.Context, keyword string) ([]VodItem, error)
	Upsert(ctx context.Context, item VodItem) error
}

type SiteStore interface {
	ListEnabled(ctx context.Context) ([]Site, error)
}

type FilterStore interface {
	CopyrightKeywords(ctx context.Context) ([]string, error)
	CategoryKeywords(ctx context.Context) ([]string, error)
}

type SourceCrawler interface {
	Search(ctx context.Context, baseURL, keyword, sourceKey string, restrictedCategories []string) ([]VodItem, error)
}

type HealthMonitor interface {
	FilterAvailable(sites []Site) (available []Site, skipped []string)
	Record(siteKey string, outcome Outcome, elapsedMilliseconds int64)
}

type BackgroundRunner interface {
	Run(task func(context.Context))
}

type SearchLogStore interface {
	Log(ctx context.Context, keyword string, userID *int, ipHash string) error
	Trending(ctx context.Context, hours, limit int) ([]TrendingKeyword, error)
}

type HealthStatStore interface {
	AddHealthStats(ctx context.Context, stats []HealthStat) error
}
