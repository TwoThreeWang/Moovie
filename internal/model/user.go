package model

import (
	"time"
)

// User 用户模型
type User struct {
	ID            int       `json:"id" db:"id"`
	Email         string    `json:"email" db:"email" gorm:"unique"`
	Username      string    `json:"username" db:"username" gorm:"unique"`
	PasswordHash  string    `json:"-" db:"password_hash"`
	Role          string    `json:"role" db:"role"`
	DoubanUserID  string    `json:"douban_user_id" db:"douban_user_id" gorm:"index"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// SessionUser 专门用于 Session 存储的用户信息结构
type SessionUser struct {
	ID       int
	Email    string
	Username string
	Role     string
}

type UserMovie struct {
	ID        int       `json:"id" db:"id"`
	UserID    int       `json:"user_id" db:"user_id" gorm:"uniqueIndex:idx_user_movie_status"`
	MovieID   string    `json:"movie_id" db:"movie_id" gorm:"uniqueIndex:idx_user_movie_status"`
	Title     string    `json:"title" db:"title"`
	Poster    string    `json:"poster" db:"poster"`
	Year      string    `json:"year" db:"year"`
	Status    string    `json:"status" db:"status" gorm:"index"`
	Rating    int       `json:"rating" db:"rating"`
	Comment   string    `json:"comment" db:"comment"`
	CreatedAt time.Time `json:"created_at" db:"created_at" gorm:"index"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at" gorm:"index"`

	// 关联
	User User `gorm:"foreignKey:UserID;references:ID"`
}

func (UserMovie) TableName() string {
	return "user_movies"
}

// WatchHistory 观影历史
type WatchHistory struct {
	ID        int       `json:"id" db:"id"`
	UserID    int       `json:"user_id" db:"user_id" gorm:"uniqueIndex:idx_user_history_vod;index:idx_user_watched"`
	DoubanID  string    `json:"douban_id" db:"douban_id" gorm:"index"`
	VodID     string    `json:"vod_id" db:"vod_id" gorm:"uniqueIndex:idx_user_history_vod"`
	Title     string    `json:"title" db:"title"`
	Poster    string    `json:"poster" db:"poster"`
	Episode   string    `json:"episode" db:"episode"`
	Progress  int       `json:"progress" db:"progress"`
	LastTime  float64   `json:"last_time" db:"last_time"`
	Duration  float64   `json:"duration" db:"duration"`
	Source    string    `json:"source" db:"source" gorm:"uniqueIndex:idx_user_history_vod"`
	WatchedAt time.Time `json:"watched_at" db:"watched_at" gorm:"index;index:idx_user_watched,sort:desc"`
}

func (WatchHistory) TableName() string {
	return "watch_histories"
}

// DoubanSyncStatus 豆瓣同步任务状态
type DoubanSyncStatus string

const (
	DoubanSyncStatusPending   DoubanSyncStatus = "pending"
	DoubanSyncStatusRunning   DoubanSyncStatus = "running"
	DoubanSyncStatusCompleted DoubanSyncStatus = "completed"
	DoubanSyncStatusFailed    DoubanSyncStatus = "failed"
)

// DoubanSyncType 豆瓣同步类型
type DoubanSyncType string

const (
	DoubanSyncTypeFull        DoubanSyncType = "full"
	DoubanSyncTypeIncremental DoubanSyncType = "incremental"
)

// DoubanSyncJob 豆瓣观影记录同步任务
type DoubanSyncJob struct {
	ID            int              `json:"id" db:"id"`
	UserID        int              `json:"user_id" db:"user_id" gorm:"index"`
	Status        DoubanSyncStatus `json:"status" db:"status" gorm:"index"`
	SyncType      DoubanSyncType   `json:"sync_type" db:"sync_type"`
	Total         int              `json:"total" db:"total"`
	Processed     int              `json:"processed" db:"processed"`
	FailedCount   int              `json:"failed_count" db:"failed_count"`
	Cursor        string           `json:"cursor" db:"cursor"`
	ErrorMessage  string           `json:"error_message" db:"error_message"`
	StartedAt     *time.Time       `json:"started_at" db:"started_at"`
	FinishedAt    *time.Time       `json:"finished_at" db:"finished_at"`
	CreatedAt     time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at" db:"updated_at"`
}

func (DoubanSyncJob) TableName() string {
	return "douban_sync_jobs"
}
