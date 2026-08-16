package feedback

import "time"

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
