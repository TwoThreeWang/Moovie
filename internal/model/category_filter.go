package model

import "time"

// CategoryFilter 分类过滤关键词
type CategoryFilter struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Keyword   string    `json:"keyword" gorm:"unique;not null"` // 过滤关键词，如 "写真"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
