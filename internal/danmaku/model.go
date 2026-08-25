// Package danmaku 负责弹幕：拉取第三方弹幕库（dandanplay 协议）并与站内弹幕合并展示。
//
// 主要涉及的表：danmakus（站内弹幕，软删除）。
//
// 弹幕按 vod_key 归档，key 形如「片名|S01|E003」，
// 因此同一部剧在不同资源站播放也能看到同一批弹幕。
package danmaku

import "time"

// Record 是一条站内弹幕。
type Record struct {
	ID        int
	VodKey    string
	Time      float64
	Text      string
	Mode      int
	Color     string
	UserID    int
	Deleted   bool
	CreatedAt time.Time
}

// Item 是返回给播放器的弹幕（Mode：0 滚动 1 顶部 2 底部）。
type Item struct {
	Text  string  `json:"text"`
	Time  float64 `json:"time"`
	Mode  int     `json:"mode"`
	Color string  `json:"color"`
}
