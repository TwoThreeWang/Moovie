// Package playback 负责播放相关功能：播放页（/play 与 /watch）、
// 播放源排序与自动换源、播放质量事件上报、首页热门榜、TVBox / IPTV 接口。
//
// 主要涉及的表：
//
//	resource_episode_candidates / resource_play_lines  播放候选（由 mediaidentity 写入）
//	playback_attempt_events  播放质量埋点
//	popularity_snapshot_runs / popularity_snapshots    热门榜快照
//
// 热门榜有三层来源，从上到下依次兜底：
//
//	数据库快照 → 组合 Provider（豆瓣优先 + TMDB 补齐）→ 各来源自己的缓存 → 陈旧缓存。
package playback

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
)

// Catalog 读写资源条目（search 包实现）。
type Catalog interface {
	Search(ctx context.Context, keyword string) ([]search.VodItem, error)
	FindBySourceID(ctx context.Context, sourceKey, vodID string) (*search.VodItem, error)
	SearchByDoubanID(ctx context.Context, doubanID string) ([]search.VodItem, error)
	Upsert(ctx context.Context, item search.VodItem) error
}

// SiteCatalog 查资源站配置。
type SiteCatalog interface {
	FindSiteByKey(ctx context.Context, key string) (*search.Site, error)
}

// DetailCrawler 抓资源站的详情（含播放地址）。
type DetailCrawler interface {
	GetDetail(ctx context.Context, baseURL, vodID, sourceKey string) (*search.VodItem, error)
}

// BackgroundRunner 跑后台任务，用于命中缓存后顺带刷新详情。
type BackgroundRunner interface {
	Run(task func(context.Context))
}

// MovieTitleFinder 用豆瓣 ID 反查片名，TVBox 找不到资源时用它再搜一次。
type MovieTitleFinder interface {
	FindTitleByDoubanID(ctx context.Context, doubanID string) (string, error)
}

// PopularProvider 是热门榜来源的统一接口，所有热门实现都满足它，因此可以自由套娃。
type PopularProvider interface {
	Popular(ctx context.Context, mediaType string) ([]PopularSubject, error)
}

// PopularIdentityResolver 把 TMDB ID 换成站内规范媒体。
type PopularIdentityResolver interface {
	FindByExternalID(ctx context.Context, provider, externalType, externalID string) (mediaidentity.Media, error)
}

// SpeedStore 读资源加载速度统计。
type SpeedStore interface {
	LoadStats(ctx context.Context, sourceKey, vodID string) (*search.LoadStats, error)
}

// CopyrightChecker 判断片名是否命中版权屏蔽词。
type CopyrightChecker interface {
	IsCopyrightRestricted(ctx context.Context, title string) (bool, string)
}

// UserMovieStore 查用户是否标记过想看/看过。
type UserMovieStore interface {
	IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error)
}
