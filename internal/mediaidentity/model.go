// Package mediaidentity 是「规范媒体身份」层，解决同一部作品在不同来源下名字、ID 各不相同的问题。
// 它给资料、资源、播放、历史提供统一的 media_id。
//
// 主要涉及的表：
//
//	media                 规范媒体主表
//	media_external_ids    外部 ID（douban / imdb / tmdb）
//	media_aliases         别名（精确匹配和模糊匹配都靠它）
//	media_units           季集（一集/一部电影是一个 unit）
//	media_field_sources   字段级来源优先级（谁写的这个字段、优先级多少）
//	media_source_snapshots 各来源最近一次抓取的记录
//	resource_media_links  资源 → 媒体的关联
//	vod_items             唯一的播放列表，线路和候选实时解析
//	playback_results       短期起播结果
package mediaidentity

import (
	"github.com/TwoThreeWang/Moovie/new/internal/mediaunits"
	"time"
)

// Media 是资料、资源和历史共同引用的规范媒体身份。
// 来源专属元数据仍保存在各自表中；该模型只保留识别和展示作品所需的稳定字段。
//
// SeriesStatus 是 TMDB 的连载状态原值（Returning Series / Ended / Canceled 等），
// 空串表示未知；电影没有这个概念。判断是否还会更新请用 SeriesEnded，不要直接比较字符串。
type Media struct {
	ID                 int
	MediaType          string
	DoubanID           string
	Title              string
	OriginalTitle      string
	Year               string
	Poster             string
	Backdrops          string
	Summary            string
	Genres             string
	Countries          string
	Directors          string
	Actors             string
	Duration           string
	RatingDouban       float64
	RatingTMDB         float64
	VoteCountTMDB      int
	SeriesStatus       string
	MetadataVersion    int
	MetadataStatus     string
	LastMetadataSyncAt time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ExternalID 是一条外部 ID 映射。(provider, external_type, external_id) 三元组唯一。
type ExternalID struct {
	MediaID      int
	Provider     string
	ExternalType string
	ExternalID   string
	Confidence   float64
	IsPrimary    bool
	VerifiedAt   time.Time
}

// Alias 是媒体的一个别名，NormalizedAlias 是归一化后的匹配键。
type Alias struct {
	MediaID         int
	Alias           string
	NormalizedAlias string
	Language        string
	Source          string
	AliasType       string
}

// MediaUnit 是可播放的最小单位：电影是一个 feature，剧集是每一集 episode。
type MediaUnit = mediaunits.Unit

// ResourceLink 是「某个资源站的某条资源」到规范媒体的关联。
// IsLocked 表示人工确认过，自动匹配不能再改它。
type ResourceLink struct {
	SourceKey  string
	VodID      string
	MediaID    int
	Confidence float64
	MatchedBy  string
	IsLocked   bool
	VerifiedAt time.Time
}

// Episode 是一条播放候选：某个资源站、某条线路、某一集的播放地址。
type Episode struct {
	CandidateKey    string
	PlaybackVersion string
	Part            string
	LineKey         string
	LineLabel       string
	LineOrder       int
	SourceKey       string
	VodID           string
	MediaID         int
	MediaUnitID     int
	UnitType        string
	SeasonNumber    int
	EpisodeKey      string
	EpisodeLabel    string
	PlayURL         string
	SortOrder       int
	Format          string
	Quality         string
	ResourceStatus  string
	LastSeenAt      time.Time
	LastAccessedAt  time.Time
}

// PlaybackAttemptEvent 只上报一次起播的最终结果；不保存播放过程明细。
type PlaybackAttemptEvent struct {
	AttemptID       string
	EventType       string
	MediaUnitID     int
	SourceKey       string
	VodID           string
	PlaybackVersion string
	ElapsedMs       int
}

// ResourceCandidate 是带质量统计的播放候选，播放页按这些数据给线路排序。
type ResourceCandidate struct {
	Episode
	SuccessCount      int
	FailureCount      int
	AvgLoadMs         int
	MappingConfidence float64
}
