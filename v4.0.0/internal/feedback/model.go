// Package feedback 是用户反馈：提交、公开列表、个人列表和后台处理。
// 只涉及一张 feedbacks 表。系统告警也走这里（Type 为「系统告警」）。
package feedback

import "time"

// 反馈类型与处理状态。
const (
	TypeBug         = "bug"
	TypeRequest     = "request"
	TypeSuggestion  = "suggestion"
	TypeDMCA        = "dmca"
	TypeSystemAlert = "系统告警"

	StatusPending  = "pending"
	StatusResolved = "resolved"
	StatusRejected = "rejected"
)

// Feedback 是一条反馈，UserID 为空表示游客提交。
type Feedback struct {
	ID        int
	UserID    *int
	Type      string
	Content   string
	MovieURL  string
	Status    string
	Reply     string
	RepliedAt *time.Time
	CreatedAt time.Time
}
