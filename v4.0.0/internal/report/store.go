package report

import "context"

type Store interface {
	Save(ctx context.Context, report MonthlyReport) (*MonthlyReport, error)
	GetByUserAndMonth(ctx context.Context, userID int, yearMonth string) (*MonthlyReport, error)
	LatestByUser(ctx context.Context, userID int) (*MonthlyReport, error)
	LatestForDashboard(ctx context.Context, userID int) (any, error)
	ListByUser(ctx context.Context, userID, limit, offset int) ([]MonthlyReport, error)
	UpdateStatus(ctx context.Context, reportID int, status Status, message string) error
}
