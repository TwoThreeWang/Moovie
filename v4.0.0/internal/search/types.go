// Package search 负责资源侧搜索：向各个 AppleCMS 资源站并发抓取、落库 vod_items、
// 把资源匹配到规范媒体（media 表），再按媒体聚合成统一搜索结果。
//
// 主要涉及的表：
//
//	vod_items              资源条目（来源站原始字段）
//	sites                  资源站清单
//	copyright_filters      版权屏蔽关键词      category_filters 分类屏蔽关键词
//	search_logs            搜索日志（热搜从此表实时聚合）
//	site_stats             资源站健康统计（熔断依据）
//	resource_media_links   资源 → 规范媒体的关联
//	resource_match_candidates  待人工复核的匹配（复核留痕走日志，不再有审计表）
package search

import (
	"strings"
	"time"
)

// Site 是一个 AppleCMS 资源站配置。
type Site struct {
	ID        uint
	Key       string
	BaseURL   string
	Enabled   bool
	CreatedAt int64
	UpdatedAt int64
}

// BaseUrl 提供给模板使用的取值方法。
func (site Site) BaseUrl() string { return site.BaseURL }

// Filter 是一条屏蔽关键词（版权屏蔽和分类屏蔽共用这个结构）。
type Filter struct {
	ID        uint
	Keyword   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// VodItem 使用资源站原始字段名，HTMX 页面与 TVBox 输出共用这一份资源模型。
type VodItem struct {
	SourceKey      string    `json:"source_key"`
	VodId          string    `json:"vod_id"`
	VodName        string    `json:"vod_name"`
	VodSub         string    `json:"vod_sub"`
	VodEn          string    `json:"vod_en"`
	VodTag         string    `json:"vod_tag"`
	VodClass       string    `json:"vod_class"`
	VodPic         string    `json:"vod_pic"`
	VodActor       string    `json:"vod_actor"`
	VodDirector    string    `json:"vod_director"`
	VodBlurb       string    `json:"vod_blurb"`
	VodRemarks     string    `json:"vod_remarks"`
	VodPubdate     string    `json:"vod_pubdate"`
	VodTotal       string    `json:"vod_total"`
	VodSerial      string    `json:"vod_serial"`
	VodArea        string    `json:"vod_area"`
	VodLang        string    `json:"vod_lang"`
	VodYear        string    `json:"vod_year"`
	VodDuration    string    `json:"vod_duration"`
	VodTime        string    `json:"vod_time"`
	VodDoubanId    string    `json:"vod_douban_id"`
	VodContent     string    `json:"vod_content"`
	VodPlayUrl     string    `json:"vod_play_url"`
	TypeName       string    `json:"type_name"`
	LastVisitedAt  time.Time `json:"last_visited_at"`
	AvgSpeedMs     int       `json:"avg_speed_ms"`
	SampleCount    int       `json:"sample_count"`
	FailedCount    int       `json:"failed_count"`
	ResourceStatus string    `json:"resource_status,omitempty"`
	// MediaID 指向规范媒体；source_key 与 vod_id 共同标识具体资源。
	MediaID         int           `json:"media_id,omitempty"`
	MediaMatch      string        `json:"media_match,omitempty"`
	MediaConfidence float64       `json:"media_confidence,omitempty"`
	PlaybackState   PlaybackState `json:"playback_state,omitempty"`
}

// PlaybackState 是全站统一的播放可用性：ready 走 /watch，direct 走 /play，
// none 表示当前没有已经确认可用的播放入口。
type PlaybackState string

const (
	PlaybackNone   PlaybackState = "none"
	PlaybackDirect PlaybackState = "direct"
	PlaybackReady  PlaybackState = "ready"
)

// PlaybackSummary 是媒体级播放摘要，搜索、详情和首页入口共用这一份判断。
type PlaybackSummary struct {
	MediaID       int
	State         PlaybackState
	ResourceCount int
	Resources     []VodItem
	BestResource  *VodItem
}

func (summary PlaybackSummary) Ready() bool  { return summary.State == PlaybackReady }
func (summary PlaybackSummary) Direct() bool { return summary.State == PlaybackDirect }
func (summary PlaybackSummary) Available() bool {
	return summary.State != PlaybackNone && summary.BestResource != nil
}

// GetGenres/GetDirectors/GetActors 把逗号分隔的字段拆成切片，供模板渲染。
func (item *VodItem) GetGenres() []string { return splitMetadata(item.VodClass) }

// GetDirectors 和 GetActors 把逗号分隔的字段拆成列表。
func (item *VodItem) GetDirectors() []string { return splitMetadata(item.VodDirector) }
func (item *VodItem) GetActors() []string    { return splitMetadata(item.VodActor) }

// splitMetadata 按逗号拆分并去掉空白项。
func splitMetadata(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// LoadStats 是某条资源的播放质量统计（首帧耗时、样本数、失败数）。
type LoadStats struct {
	AvgSpeedMs  int     `json:"avg_speed_ms"`
	SampleCount int     `json:"sample_count"`
	FailedCount int     `json:"failed_count"`
	SuccessRate float64 `json:"success_rate"`
}

// Result 是一次资源搜索的结果，FilteredCount 表示被版权关键词过滤掉的条数。
type Result struct {
	Items         []VodItem
	FilteredCount int
}

// TrendingKeyword 是热搜榜的原始统计行。
type TrendingKeyword struct {
	Keyword        string
	Count          int
	LastSearchedAt time.Time
}

// TrendItem 是热搜榜渲染用的条目，Tag 是“热/新/爆”这类角标。
type TrendItem struct {
	Keyword  string
	Count    int
	Tag      string
	TagClass string
}

// HealthStat 是资源站在某个小时桶内的抓取结果计数，落在 site_stats 表。
type HealthStat struct {
	SiteKey      string
	Bucket       time.Time
	OKCount      int
	EmptyCount   int
	TimeoutCount int
	ErrorCount   int
	TotalMs      int64
}

// HealthSummary 是后台展示用的资源站健康汇总，附带熔断状态。
type HealthSummary struct {
	SiteKey      string
	OKCount      int
	EmptyCount   int
	TimeoutCount int
	ErrorCount   int
	TotalMs      int64
	Tripped      bool
	TrippedUntil time.Time
}

// Total 返回样本总数，下面几个比率方法都以它为分母。
func (summary *HealthSummary) Total() int {
	if summary == nil {
		return 0
	}
	return summary.OKCount + summary.EmptyCount + summary.TimeoutCount + summary.ErrorCount
}

// HasData 判断是否有统计样本。
func (summary *HealthSummary) HasData() bool { return summary.Total() > 0 }

// OKRate 返回有结果的比例（百分比）。
func (summary *HealthSummary) OKRate() float64 {
	if summary.Total() == 0 {
		return 0
	}
	return float64(summary.OKCount) * 100 / float64(summary.Total())
}

// EmptyRate 返回返回空列表的比例（百分比）。
func (summary *HealthSummary) EmptyRate() float64 {
	if summary.Total() == 0 {
		return 0
	}
	return float64(summary.EmptyCount) * 100 / float64(summary.Total())
}

// FailRate 返回超时与报错合计的比例（百分比）。
func (summary *HealthSummary) FailRate() float64 {
	if summary.Total() == 0 {
		return 0
	}
	return float64(summary.TimeoutCount+summary.ErrorCount) * 100 / float64(summary.Total())
}

// AvgMs 返回平均响应耗时。
func (summary *HealthSummary) AvgMs() int {
	if summary.Total() == 0 {
		return 0
	}
	return int(summary.TotalMs / int64(summary.Total()))
}

// Level 把比率折算成 good/warn/bad 三档，供后台用颜色展示。
func (summary *HealthSummary) Level() string {
	if !summary.HasData() {
		return "none"
	}
	switch {
	case summary.OKRate() < 50 || summary.EmptyRate() > 90:
		return "bad"
	case summary.OKRate() < 80 || summary.FailRate() > 20:
		return "warn"
	default:
		return "good"
	}
}

// Outcome 是一次资源站抓取的结果分类，同时用于健康统计和熔断判断。
type Outcome string

// 一次资源站请求的四种结果。
const (
	OutcomeOK      Outcome = "ok"
	OutcomeEmpty   Outcome = "empty"
	OutcomeTimeout Outcome = "timeout"
	OutcomeError   Outcome = "error"
)
