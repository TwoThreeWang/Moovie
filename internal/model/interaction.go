package model

import (
	"time"
)

// SearchLog 搜索日志
type SearchLog struct {
	ID        int       `json:"id" db:"id"`
	Keyword   string    `json:"keyword" db:"keyword"`
	UserID    *int      `json:"user_id" db:"user_id"`
	IPHash    string    `json:"ip_hash" db:"ip_hash"`
	CreatedAt time.Time `json:"created_at" db:"created_at" gorm:"index"`
}

// Feedback 反馈
type Feedback struct {
	ID        int        `json:"id" db:"id"`
	UserID    *int       `json:"user_id" db:"user_id"`
	Type      string     `json:"type" db:"type"`
	Content   string     `json:"content" db:"content"`
	MovieURL  string     `json:"movie_url" db:"movie_url"`
	Status    string     `json:"status" db:"status"`
	Reply     string     `json:"reply" db:"reply"`
	RepliedAt *time.Time `json:"replied_at" db:"replied_at"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// TrendingKeyword 热搜关键词
type TrendingKeyword struct {
	Keyword        string    `json:"keyword" db:"keyword" gorm:"primaryKey"`
	Count          int       `json:"count" db:"count"`
	LastSearchedAt time.Time `json:"last_searched_at" db:"last_searched_at"`
}
