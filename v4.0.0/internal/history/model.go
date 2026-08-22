// Package history 负责观看记录与播放进度：多设备同步、继续观看列表、首页今日更新。
//
// 主要涉及的表：
//
//	playback_positions        播放进度主表（每个用户每一集一行，唯一的服务端进度来源）
//	playback_position_version_seq  全局版本号序列，用于增量同步
//	history_sync_events       同步事件账本（只追加，客户端按游标增量拉取）
//	user_movies               片单标记，参与「继续观看」的过滤
//
// 同步模型：客户端提交一批操作 → 服务端按 operation_id 去重（幂等）→ 写进度 →
// 追加一条事件 → 返回大于客户端游标的所有事件。以「谁的时间更新听谁的」解决冲突。
package history

import "time"

// Record 是一条播放进度记录，同时用作接口返回结构。
type Record struct {
	ID              int       `json:"id"`
	UserID          int       `json:"user_id"`
	MediaID         int       `json:"media_id,omitempty"`
	MediaUnitID     int       `json:"media_unit_id,omitempty"`
	DoubanID        string    `json:"douban_id"`
	VodID           string    `json:"vod_id"`
	Title           string    `json:"title"`
	Poster          string    `json:"poster"`
	Episode         string    `json:"episode"`
	SeasonNumber    int       `json:"season_number,omitempty"`
	EpisodeKey      string    `json:"episode_key,omitempty"`
	Progress        int       `json:"progress"`
	LastTime        float64   `json:"last_time"`
	Duration        float64   `json:"duration"`
	Source          string    `json:"source"`
	PreferredSource string    `json:"preferred_source_key,omitempty"`
	PreferredVodID  string    `json:"preferred_vod_id,omitempty"`
	EntryPage       string    `json:"entry_page"`
	WatchedAt       time.Time `json:"watched_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
