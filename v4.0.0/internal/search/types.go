package search

import (
	"strings"
	"time"
)

type Site struct {
	ID        uint
	Key       string
	BaseURL   string
	Enabled   bool
	CreatedAt int64
	UpdatedAt int64
}

func (site Site) BaseUrl() string { return site.BaseURL }

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
	MediaID         int     `json:"media_id,omitempty"`
	MediaMatch      string  `json:"media_match,omitempty"`
	MediaConfidence float64 `json:"media_confidence,omitempty"`
}

func (item *VodItem) GetGenres() []string    { return splitMetadata(item.VodClass) }
func (item *VodItem) GetDirectors() []string { return splitMetadata(item.VodDirector) }
func (item *VodItem) GetActors() []string    { return splitMetadata(item.VodActor) }

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

type LoadStats struct {
	AvgSpeedMs  int     `json:"avg_speed_ms"`
	SampleCount int     `json:"sample_count"`
	FailedCount int     `json:"failed_count"`
	SuccessRate float64 `json:"success_rate"`
}

type Result struct {
	Items         []VodItem
	FilteredCount int
}

type TrendingKeyword struct {
	Keyword        string
	Count          int
	LastSearchedAt time.Time
}

type TrendItem struct {
	Keyword  string
	Count    int
	Tag      string
	TagClass string
}

type HealthStat struct {
	SiteKey      string
	Bucket       time.Time
	OKCount      int
	EmptyCount   int
	TimeoutCount int
	ErrorCount   int
	TotalMs      int64
}

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

func (summary *HealthSummary) Total() int {
	if summary == nil {
		return 0
	}
	return summary.OKCount + summary.EmptyCount + summary.TimeoutCount + summary.ErrorCount
}

func (summary *HealthSummary) HasData() bool { return summary.Total() > 0 }

func (summary *HealthSummary) OKRate() float64 {
	if summary.Total() == 0 {
		return 0
	}
	return float64(summary.OKCount) * 100 / float64(summary.Total())
}

func (summary *HealthSummary) EmptyRate() float64 {
	if summary.Total() == 0 {
		return 0
	}
	return float64(summary.EmptyCount) * 100 / float64(summary.Total())
}

func (summary *HealthSummary) FailRate() float64 {
	if summary.Total() == 0 {
		return 0
	}
	return float64(summary.TimeoutCount+summary.ErrorCount) * 100 / float64(summary.Total())
}

func (summary *HealthSummary) AvgMs() int {
	if summary.Total() == 0 {
		return 0
	}
	return int(summary.TotalMs / int64(summary.Total()))
}

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

type Outcome string

const (
	OutcomeOK      Outcome = "ok"
	OutcomeEmpty   Outcome = "empty"
	OutcomeTimeout Outcome = "timeout"
	OutcomeError   Outcome = "error"
)
