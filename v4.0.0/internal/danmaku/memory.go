package danmaku

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int
	records []Record
	now     func() time.Time
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{nextID: 1, now: time.Now} }

func (store *MemoryStore) ListByVodKey(_ context.Context, vodKey string, limit int) ([]Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Record, 0)
	for _, record := range store.records {
		if record.VodKey == vodKey && !record.Deleted {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Time < records[j].Time })
	if limit >= 0 && len(records) > limit {
		records = records[:limit]
	}
	return append([]Record(nil), records...), nil
}

func (store *MemoryStore) CreateGuarded(_ context.Context, record Record, rateSince, duplicateSince time.Time, maxPerWindow int) (*Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, existing := range store.records {
		if existing.UserID == record.UserID && !existing.CreatedAt.Before(rateSince) {
			count++
		}
	}
	if count >= maxPerWindow {
		return nil, ErrRateLimited
	}
	for _, existing := range store.records {
		if existing.UserID == record.UserID && existing.VodKey == record.VodKey && existing.Text == record.Text && !existing.CreatedAt.Before(duplicateSince) {
			return nil, ErrDuplicate
		}
	}
	record.ID = store.nextID
	store.nextID++
	if record.CreatedAt.IsZero() {
		record.CreatedAt = store.now()
	}
	store.records = append(store.records, record)
	copy := record
	return &copy, nil
}

func (store *MemoryStore) SoftDelete(_ context.Context, id int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.records {
		if store.records[index].ID == id {
			store.records[index].Deleted = true
			break
		}
	}
	return nil
}

func (store *MemoryStore) ListRecent(_ context.Context, limit, offset int) ([]Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Record, 0)
	for _, record := range store.records {
		if !record.Deleted {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	if offset < 0 {
		offset = 0
	}
	if offset >= len(records) {
		return []Record{}, nil
	}
	end := len(records)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]Record(nil), records[offset:end]...), nil
}
