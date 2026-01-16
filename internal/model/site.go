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

// VodItem 资源网视频数据（所有字段统一为 string）
type VodItem struct {
	SourceKey     string    `json:"source_key" db:"source_key"`           // 来源站点Key
	VodId         string    `json:"vod_id" db:"vod_id"`                   // 视频ID
	VodName       string    `json:"vod_name" db:"vod_name"`               // 名称
	VodSub        string    `json:"vod_sub" db:"vod_sub"`                 // 副标题
	VodEn         string    `json:"vod_en" db:"vod_en"`                   // 英文名
	VodTag        string    `json:"vod_tag" db:"vod_tag"`                 // 标签
	VodClass      string    `json:"vod_class" db:"vod_class"`             // 分类
	VodPic        string    `json:"vod_pic" db:"vod_pic"`                 // 封面图
	VodActor      string    `json:"vod_actor" db:"vod_actor"`             // 演员
	VodDirector   string    `json:"vod_director" db:"vod_director"`       // 导演
	VodBlurb      string    `json:"vod_blurb" db:"vod_blurb"`             // 简介
	VodRemarks    string    `json:"vod_remarks" db:"vod_remarks"`         // 备注（如"第27集完结"）
	VodPubdate    string    `json:"vod_pubdate" db:"vod_pubdate"`         // 上映日期
	VodTotal      string    `json:"vod_total" db:"vod_total"`             // 总集数
	VodSerial     string    `json:"vod_serial" db:"vod_serial"`           // 连载状态
	VodArea       string    `json:"vod_area" db:"vod_area"`               // 地区
	VodLang       string    `json:"vod_lang" db:"vod_lang"`               // 语言
	VodYear       string    `json:"vod_year" db:"vod_year"`               // 年份
	VodDuration   string    `json:"vod_duration" db:"vod_duration"`       // 时长
	VodTime       string    `json:"vod_time" db:"vod_time"`               // 更新时间
	VodDoubanId   string    `json:"vod_douban_id" db:"vod_douban_id"`     // 豆瓣ID
	VodContent    string    `json:"vod_content" db:"vod_content"`         // 详细内容
	VodPlayUrl    string    `json:"vod_play_url" db:"vod_play_url"`       // 播放链接
	TypeName      string    `json:"type_name" db:"type_name"`             // 类型名称
	LastVisitedAt time.Time `json:"last_visited_at" db:"last_visited_at"` // 最后访问时间
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
