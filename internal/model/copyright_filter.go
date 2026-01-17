package model

import "time"

// CopyrightFilter 版权限制关键词
type CopyrightFilter struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Keyword   string    `json:"keyword" gorm:"unique;not null"` // 版权限制关键词
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
