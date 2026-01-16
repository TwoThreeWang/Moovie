package model

import (
	"time"
)

// User 用户模型
type User struct {
	ID           int       `json:"id" db:"id"`
	Email        string    `json:"email" db:"email" gorm:"unique"`
	Username     string    `json:"username" db:"username" gorm:"unique"`
	PasswordHash string    `json:"-" db:"password_hash"`
	Role         string    `json:"role" db:"role"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// SessionUser 专门用于 Session 存储的用户信息结构
type SessionUser struct {
	ID       int
	Email    string
	Username string
	Role     string
}

// Favorite 收藏
type Favorite struct {
	ID        int       `json:"id" db:"id"`
	UserID    int       `json:"user_id" db:"user_id"`
	MovieID   int       `json:"movie_id" db:"movie_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	Movie     *Movie    `json:"movie,omitempty"` // 关联查询时填充
}

// WatchHistory 观影历史
type WatchHistory struct {
	ID        int       `json:"id" db:"id"`
	UserID    int       `json:"user_id" db:"user_id" gorm:"uniqueIndex:idx_user_history_vod"`
	DoubanID  string    `json:"douban_id" db:"douban_id"`
	VodID     string    `json:"vod_id" db:"vod_id" gorm:"uniqueIndex:idx_user_history_vod"`
	Title     string    `json:"title" db:"title"`
	Poster    string    `json:"poster" db:"poster"`
	Episode   string    `json:"episode" db:"episode" gorm:"uniqueIndex:idx_user_history_vod"`
	Progress  int       `json:"progress" db:"progress"`
	LastTime  float64   `json:"last_time" db:"last_time"`
	Duration  float64   `json:"duration" db:"duration"`
	Source    string    `json:"source" db:"source" gorm:"uniqueIndex:idx_user_history_vod"`
	WatchedAt time.Time `json:"watched_at" db:"watched_at"`
}
