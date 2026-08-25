package feedback

import "context"

// Store 是反馈的读写接口。
type Store interface {
	Create(ctx context.Context, feedback Feedback) (*Feedback, error)
	ListPublic(ctx context.Context, feedbackType string, limit, offset int) ([]Feedback, error)
	CountPublic(ctx context.Context, feedbackType string) (int, error)
	ListByUser(ctx context.Context, userID, limit, offset int) ([]Feedback, error)
	CountByUser(ctx context.Context, userID int) (int, error)
	ListAdmin(ctx context.Context, status string, limit, offset int) ([]Feedback, error)
	CountPending(ctx context.Context) (int, error)
	UpdateStatus(ctx context.Context, id int, status string) error
	Reply(ctx context.Context, id int, reply string) error
}
