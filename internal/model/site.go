package model

import (
	"strings"
	"time"
)

// Site 爬虫站点配置
type Site struct {
	ID        uint   `json:"id" db:"id"`
	Key       string `json:"key" db:"key" gorm:"unique"` // 网站简称
	BaseUrl   string `json:"base_url" db:"base_url"`     // 基础URL
	Enabled   bool   `json:"enabled" db:"enabled"`       // 是否启用
	CreatedAt int64  `json:"created_at" db:"created_at"`
	UpdatedAt int64  `json:"updated_at" db:"updated_at"`
}

// SiteCallOutcome 一次采集调用的结果分类
type SiteCallOutcome string

const (
	// SiteCallOK 返回了至少一条结果
	SiteCallOK SiteCallOutcome = "ok"
	// SiteCallEmpty 请求与解析都成功，但返回 0 条。
	// 单独区分是因为采集站改字段名时 HTTP 200 且 JSON 可解析，
	// 只看成功率完全发现不了，只有空返回率能暴露这种静默失效。
	SiteCallEmpty SiteCallOutcome = "empty"
	// SiteCallTimeout 请求超时
	SiteCallTimeout SiteCallOutcome = "timeout"
	// SiteCallError 网络错误 / 非 200 / JSON 解析失败
	SiteCallError SiteCallOutcome = "error"
)

// SiteStat 采集站点健康度统计，按「站点 + 小时」分桶累加。
// 写入不在请求路径上：内存累加，由后台 goroutine 定时 flush。
type SiteStat struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	SiteKey      string    `json:"site_key" db:"site_key" gorm:"uniqueIndex:idx_site_stats_bucket"`
	Bucket       time.Time `json:"bucket" db:"bucket" gorm:"uniqueIndex:idx_site_stats_bucket;index"` // 截断到整点
	OKCount      int       `json:"ok_count" db:"ok_count"`
	EmptyCount   int       `json:"empty_count" db:"empty_count"`
	TimeoutCount int       `json:"timeout_count" db:"timeout_count"`
	ErrorCount   int       `json:"error_count" db:"error_count"`
	TotalMs      int64     `json:"total_ms" db:"total_ms"` // 累计耗时，除以 Total() 得均值
}

func (SiteStat) TableName() string {
	return "site_stats"
}

// Total 总调用次数
func (s *SiteStat) Total() int {
	return s.OKCount + s.EmptyCount + s.TimeoutCount + s.ErrorCount
}

// SiteStatSummary 某站点一段时间内的汇总，用于后台展示
type SiteStatSummary struct {
	SiteKey      string `json:"site_key" gorm:"column:site_key"`
	OKCount      int    `json:"ok_count" gorm:"column:ok_count"`
	EmptyCount   int    `json:"empty_count" gorm:"column:empty_count"`
	TimeoutCount int    `json:"timeout_count" gorm:"column:timeout_count"`
	ErrorCount   int    `json:"error_count" gorm:"column:error_count"`
	TotalMs      int64  `json:"total_ms" gorm:"column:total_ms"`

	// Tripped / TrippedUntil 由内存中的熔断器填充，不来自数据库
	Tripped      bool      `json:"tripped" gorm:"-"`
	TrippedUntil time.Time `json:"tripped_until" gorm:"-"`
}

// Total 总调用次数
func (s *SiteStatSummary) Total() int {
	return s.OKCount + s.EmptyCount + s.TimeoutCount + s.ErrorCount
}

// HasData 是否有采样数据
func (s *SiteStatSummary) HasData() bool {
	return s.Total() > 0
}

// OKRate 成功率（返回到结果的比例），0-100
func (s *SiteStatSummary) OKRate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return float64(s.OKCount) * 100 / float64(s.Total())
}

// EmptyRate 空返回率，0-100。持续偏高说明站点接口结构可能已变更
func (s *SiteStatSummary) EmptyRate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return float64(s.EmptyCount) * 100 / float64(s.Total())
}

// FailRate 失败率（超时 + 错误），0-100
func (s *SiteStatSummary) FailRate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return float64(s.TimeoutCount+s.ErrorCount) * 100 / float64(s.Total())
}

// AvgMs 平均耗时（毫秒）
func (s *SiteStatSummary) AvgMs() int {
	if s.Total() == 0 {
		return 0
	}
	return int(s.TotalMs / int64(s.Total()))
}

// Level 健康等级：good / warn / bad，供模板直接当 CSS 类名用
func (s *SiteStatSummary) Level() string {
	if !s.HasData() {
		return "none"
	}
	switch {
	case s.OKRate() < 50 || s.EmptyRate() > 90:
		return "bad"
	case s.OKRate() < 80 || s.FailRate() > 20:
		return "warn"
	default:
		return "good"
	}
}

// VodItem 资源网视频数据（所有字段统一为 string）
type VodItem struct {
	SourceKey     string    `json:"source_key" db:"source_key" gorm:"uniqueIndex:idx_source_vod"` // 来源站点Key
	VodId         string    `json:"vod_id" db:"vod_id" gorm:"uniqueIndex:idx_source_vod"`         // 视频ID
	VodName       string    `json:"vod_name" db:"vod_name"`                                       // 名称
	VodSub        string    `json:"vod_sub" db:"vod_sub"`                                         // 副标题
	VodEn         string    `json:"vod_en" db:"vod_en"`                                           // 英文名
	VodTag        string    `json:"vod_tag" db:"vod_tag"`                                         // 标签
	VodClass      string    `json:"vod_class" db:"vod_class"`                                     // 分类
	VodPic        string    `json:"vod_pic" db:"vod_pic"`                                         // 封面图
	VodActor      string    `json:"vod_actor" db:"vod_actor"`                                     // 演员
	VodDirector   string    `json:"vod_director" db:"vod_director"`                               // 导演
	VodBlurb      string    `json:"vod_blurb" db:"vod_blurb"`                                     // 简介
	VodRemarks    string    `json:"vod_remarks" db:"vod_remarks"`                                 // 备注（如"第27集完结"）
	VodPubdate    string    `json:"vod_pubdate" db:"vod_pubdate"`                                 // 上映日期
	VodTotal      string    `json:"vod_total" db:"vod_total"`                                     // 总集数
	VodSerial     string    `json:"vod_serial" db:"vod_serial"`                                   // 连载状态
	VodArea       string    `json:"vod_area" db:"vod_area"`                                       // 地区
	VodLang       string    `json:"vod_lang" db:"vod_lang"`                                       // 语言
	VodYear       string    `json:"vod_year" db:"vod_year"`                                       // 年份
	VodDuration   string    `json:"vod_duration" db:"vod_duration"`                               // 时长
	VodTime       string    `json:"vod_time" db:"vod_time"`                                       // 更新时间
	VodDoubanId   string    `json:"vod_douban_id" db:"vod_douban_id"`                             // 豆瓣ID
	VodContent    string    `json:"vod_content" db:"vod_content"`                                 // 详细内容
	VodPlayUrl    string    `json:"vod_play_url" db:"vod_play_url"`                               // 播放链接
	TypeName      string    `json:"type_name" db:"type_name"`                                     // 类型名称
	LastVisitedAt time.Time `json:"last_visited_at" db:"last_visited_at" gorm:"index"`            // 最后访问时间
	AvgSpeedMs    int       `json:"avg_speed_ms" db:"avg_speed_ms"`                               // 平均加载耗时(毫秒)
	SampleCount   int       `json:"sample_count" db:"sample_count"`                               // 样本数量
	FailedCount   int       `json:"failed_count" db:"failed_count"`                               // 失败次数
}

// GetGenres 获取分类切片
func (v *VodItem) GetGenres() []string {
	if v.VodClass == "" {
		return nil
	}
	res := []string{}
	parts := strings.Split(v.VodClass, ",")
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			res = append(res, s)
		}
	}
	return res
}

// GetDirectors 获取导演切片
func (v *VodItem) GetDirectors() []string {
	if v.VodDirector == "" {
		return nil
	}
	res := []string{}
	parts := strings.Split(v.VodDirector, ",")
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			res = append(res, s)
		}
	}
	return res
}

// GetActors 获取演员切片
func (v *VodItem) GetActors() []string {
	if v.VodActor == "" {
		return nil
	}
	res := []string{}
	parts := strings.Split(v.VodActor, ",")
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			res = append(res, s)
		}
	}
	return res
}
