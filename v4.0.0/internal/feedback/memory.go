package feedback

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int
	records []Feedback
	now     func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, now: time.Now}
}

func (store *MemoryStore) Create(_ context.Context, record Feedback) (*Feedback, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record.ID = store.nextID
	store.nextID++
	record.Status = StatusPending
	if record.CreatedAt.IsZero() {
		record.CreatedAt = store.now()
	}
	store.records = append(store.records, record)
	copy := record
	return &copy, nil
}

func (store *MemoryStore) ListPublic(_ context.Context, feedbackType string, limit, offset int) ([]Feedback, error) {
	return store.list(func(record Feedback) bool { return feedbackType == "" || record.Type == feedbackType }, limit, offset), nil
}

func (store *MemoryStore) CountPublic(_ context.Context, feedbackType string) (int, error) {
	return store.count(func(record Feedback) bool { return feedbackType == "" || record.Type == feedbackType }), nil
}

func (store *MemoryStore) ListByUser(_ context.Context, userID, limit, offset int) ([]Feedback, error) {
	return store.list(func(record Feedback) bool { return record.UserID != nil && *record.UserID == userID }, limit, offset), nil
}

func (store *MemoryStore) CountByUser(_ context.Context, userID int) (int, error) {
	return store.count(func(record Feedback) bool { return record.UserID != nil && *record.UserID == userID }), nil
}

func (store *MemoryStore) ListAdmin(_ context.Context, status string, limit, offset int) ([]Feedback, error) {
	return store.list(func(record Feedback) bool { return status == "" || record.Status == status }, limit, offset), nil
}

func (store *MemoryStore) CountPending(_ context.Context) (int, error) {
	return store.count(func(record Feedback) bool { return record.Status == StatusPending }), nil
}

func (store *MemoryStore) UpdateStatus(_ context.Context, id int, status string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.records {
		if store.records[index].ID == id {
			store.records[index].Status = status
			return nil
		}
	}
	return nil
}

func (store *MemoryStore) Reply(_ context.Context, id int, reply string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.records {
		if store.records[index].ID == id {
			now := store.now()
			store.records[index].Reply = reply
			store.records[index].RepliedAt = &now
			store.records[index].Status = StatusResolved
			return nil
		}
	}
	return nil
}

func (store *MemoryStore) list(include func(Feedback) bool, limit, offset int) []Feedback {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Feedback, 0)
	for _, record := range store.records {
		if include(record) {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if offset < 0 {
		offset = 0
	}
	if offset >= len(records) {
		return []Feedback{}
	}
	end := len(records)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]Feedback(nil), records[offset:end]...)
}

func (store *MemoryStore) count(include func(Feedback) bool) int {
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, record := range store.records {
		if include(record) {
			count++
		}
	}
	return count
}
