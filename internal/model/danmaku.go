package model

import "time"

// Danmaku 站内用户发送的弹幕。
//
// VodKey 是「片名|季|集」归一化后的标识（由 handler 侧的 splitSeason/parseEpisodeNumber 生成），
// 刻意不用 source_key + vod_id：同一集经常同时存在于多个采集源，
// 用片名维度归属才能让弹幕在不同线路之间共享，人气才攒得起来。
type Danmaku struct {
	ID        int       `json:"id" db:"id"`
	VodKey    string    `json:"vod_key" db:"vod_key" gorm:"size:255;index:idx_danmaku_lookup,priority:1"`
	Time      float64   `json:"time" db:"time"`                          // 出现时间（秒）
	Text      string    `json:"text" db:"text" gorm:"size:100"`          // 弹幕文本
	Mode      int       `json:"mode" db:"mode"`                          // 0 滚动 / 1 顶部 / 2 底部
	Color     string    `json:"color" db:"color" gorm:"size:7"`          // #RRGGBB
	UserID    int       `json:"user_id" db:"user_id" gorm:"index"`       // 发送者，用于溯源和封禁
	Deleted   bool      `json:"deleted" db:"deleted" gorm:"default:false;index:idx_danmaku_lookup,priority:2"`
	CreatedAt time.Time `json:"created_at" db:"created_at" gorm:"index"`
}
