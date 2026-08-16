package report

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int
	reports []MonthlyReport
	now     func() time.Time
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{nextID: 1, now: time.Now} }

func (store *MemoryStore) Save(_ context.Context, report MonthlyReport) (*MonthlyReport, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	for index := range store.reports {
		if store.reports[index].UserID == report.UserID && store.reports[index].YearMonth == report.YearMonth {
			report.ID = store.reports[index].ID
			report.CreatedAt = store.reports[index].CreatedAt
			report.UpdatedAt = now
			if report.Status == StatusGenerated && report.GeneratedAt == nil {
				report.GeneratedAt = &now
			}
			store.reports[index] = report
			copy := report
			return &copy, nil
		}
	}
	report.ID = store.nextID
	store.nextID++
	if report.Status == "" {
		report.Status = StatusPending
	}
	report.CreatedAt, report.UpdatedAt = now, now
	if report.Status == StatusGenerated && report.GeneratedAt == nil {
		report.GeneratedAt = &now
	}
	store.reports = append(store.reports, report)
	copy := report
	return &copy, nil
}

func (store *MemoryStore) GetByUserAndMonth(_ context.Context, userID int, yearMonth string) (*MonthlyReport, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, report := range store.reports {
		if report.UserID == userID && report.YearMonth == yearMonth {
			copy := report
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) LatestByUser(_ context.Context, userID int) (*MonthlyReport, error) {
	reports, _ := store.ListByUser(context.Background(), userID, 1, 0)
	if len(reports) == 0 {
		return nil, nil
	}
	copy := reports[0]
	return &copy, nil
}

func (store *MemoryStore) LatestForDashboard(ctx context.Context, userID int) (any, error) {
	return store.LatestByUser(ctx, userID)
}

func (store *MemoryStore) ListByUser(_ context.Context, userID, limit, offset int) ([]MonthlyReport, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	reports := make([]MonthlyReport, 0)
	for _, report := range store.reports {
		if report.UserID == userID && report.Status == StatusGenerated {
			reports = append(reports, report)
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].YearMonth > reports[j].YearMonth })
	if offset >= len(reports) {
		return []MonthlyReport{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	end := len(reports)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]MonthlyReport(nil), reports[offset:end]...), nil
}

func (store *MemoryStore) UpdateStatus(_ context.Context, reportID int, status Status, message string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.reports {
		if store.reports[index].ID == reportID {
			now := store.now()
			store.reports[index].Status = status
			store.reports[index].ErrorMessage = message
			store.reports[index].UpdatedAt = now
			if status == StatusGenerated {
				store.reports[index].GeneratedAt = &now
			}
			return nil
		}
	}
	return nil
}
