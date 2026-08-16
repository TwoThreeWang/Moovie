package library

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

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, now: time.Now}
}

func (store *MemoryStore) Upsert(_ context.Context, record Record) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now()
	hasExternalTime := !record.CreatedAt.IsZero() || !record.UpdatedAt.IsZero()
	for index := range store.records {
		existing := &store.records[index]
		if existing.UserID == record.UserID && existing.MovieID == record.MovieID {
			record.ID = existing.ID
			if !hasExternalTime {
				record.CreatedAt = existing.CreatedAt
				record.UpdatedAt = now
			} else {
				if record.CreatedAt.IsZero() {
					record.CreatedAt = now
				}
				if record.UpdatedAt.IsZero() {
					record.UpdatedAt = now
				}
			}
			store.records[index] = record
			return nil
		}
	}
	record.ID = store.nextID
	store.nextID++
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	store.records = append(store.records, record)
	return nil
}

func (store *MemoryStore) ListByUserAndDateRange(_ context.Context, userID int, status string, start, end time.Time) ([]Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Record, 0)
	for _, record := range store.records {
		if record.UserID == userID && record.Status == status && !record.CreatedAt.Before(start) && record.CreatedAt.Before(end) {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].UpdatedAt.After(records[j].UpdatedAt) })
	return records, nil
}

func (store *MemoryStore) CountWatchedByAllUsersInRange(_ context.Context, start, end time.Time) (map[int]int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	counts := make(map[int]int)
	for _, record := range store.records {
		if record.Status == StatusWatched && !record.CreatedAt.Before(start) && record.CreatedAt.Before(end) {
			counts[record.UserID]++
		}
	}
	return counts, nil
}

func (store *MemoryStore) AvgRatingByUser(_ context.Context, userID int) (float64, int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	total, count := 0, 0
	for _, record := range store.records {
		if record.UserID == userID && record.Status == StatusWatched && record.Rating > 0 {
			total += record.Rating
			count++
		}
	}
	if count == 0 {
		return 0, 0, nil
	}
	return float64(total) / float64(count), count, nil
}

func (store *MemoryStore) CountOverlapWatched(_ context.Context, userA, userB int) (int, error) {
	if userA == userB {
		return 0, nil
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	first := make(map[string]bool)
	for _, record := range store.records {
		if record.UserID == userA && record.Status == StatusWatched {
			first[record.MovieID] = true
		}
	}
	count := 0
	for _, record := range store.records {
		if record.UserID == userB && record.Status == StatusWatched && first[record.MovieID] {
			count++
		}
	}
	return count, nil
}

func (store *MemoryStore) CountByUserAndDateRange(_ context.Context, userID int, status string, start, end time.Time) (int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, record := range store.records {
		if record.UserID == userID && record.Status == status && !record.CreatedAt.Before(start) && record.CreatedAt.Before(end) {
			count++
		}
	}
	return count, nil
}

func (store *MemoryStore) Remove(_ context.Context, userID int, movieID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.records {
		if store.records[index].UserID == userID && store.records[index].MovieID == movieID {
			store.records = append(store.records[:index], store.records[index+1:]...)
			break
		}
	}
	return nil
}

func (store *MemoryStore) GetByUserAndMovie(_ context.Context, userID int, movieID string) (*Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, record := range store.records {
		if record.UserID == userID && record.MovieID == movieID {
			copy := record
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) GetByID(_ context.Context, userID, id int) (*Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, record := range store.records {
		if record.UserID == userID && record.ID == id {
			copy := record
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error) {
	record, err := store.GetByUserAndMovie(ctx, userID, movieID)
	return record != nil && record.Status == status, err
}

func (store *MemoryStore) ListByUser(_ context.Context, userID int, status string, limit, offset int) ([]Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Record, 0)
	for _, record := range store.records {
		if record.UserID == userID && record.Status == status {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].UpdatedAt.Equal(records[j].UpdatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})
	if offset >= len(records) {
		return []Record{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	end := len(records)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]Record(nil), records[offset:end]...), nil
}

func (store *MemoryStore) CountByUser(_ context.Context, userID int, status string) (int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, record := range store.records {
		if record.UserID == userID && record.Status == status {
			count++
		}
	}
	return count, nil
}

func (store *MemoryStore) CountByMovie(_ context.Context, movieID, status string) (int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, record := range store.records {
		if record.MovieID == movieID && record.Status == status {
			count++
		}
	}
	return count, nil
}

func (store *MemoryStore) UpdateRatingComment(_ context.Context, userID, id, rating int, comment string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.records {
		if store.records[index].UserID == userID && store.records[index].ID == id {
			store.records[index].Rating = rating
			store.records[index].Comment = comment
			store.records[index].UpdatedAt = store.now()
			break
		}
	}
	return nil
}
