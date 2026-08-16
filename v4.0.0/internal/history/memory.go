package history

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.RWMutex
	nextID      int
	nextVersion int64
	records     []Record
	events      []memorySyncEvent
	operations  map[string]int64
}

type memorySyncEvent struct {
	userID  int
	version int64
	payload syncEventPayload
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, nextVersion: 1, operations: make(map[string]int64)}
}

func (store *MemoryStore) Upsert(_ context.Context, record Record) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.upsertLocked(record)
	return nil
}

func (store *MemoryStore) ListContinue(_ context.Context, userID, limit, offset int) ([]Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Record, 0)
	for _, record := range store.records {
		if record.UserID == userID && record.Progress < 100 {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].WatchedAt.After(records[j].WatchedAt) })
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

func (store *MemoryStore) ListByUser(_ context.Context, userID, limit, offset int) ([]Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]Record, 0)
	for _, record := range store.records {
		if record.UserID == userID {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].WatchedAt.After(records[j].WatchedAt) })
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

func (store *MemoryStore) CountByUser(_ context.Context, userID int) (int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	count := 0
	for _, record := range store.records {
		if record.UserID == userID {
			count++
		}
	}
	return count, nil
}

func (store *MemoryStore) SyncV2(_ context.Context, userID int, request SyncV2Request, _ time.Time) (SyncV2Result, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.bootstrapSyncEventsLocked(userID)
	for _, operation := range request.Operations {
		operationKey := syncOperationKey(userID, request.DeviceID, operation.OperationID)
		if _, duplicate := store.operations[operationKey]; duplicate {
			continue
		}
		version := store.nextVersion
		store.nextVersion++
		store.operations[operationKey] = version

		currentIndex := store.findOperationRecordLocked(userID, operation)
		var current *Record
		if currentIndex >= 0 {
			copy := store.records[currentIndex]
			current = &copy
		}
		if current != nil && !operation.force && recordTime(*current).After(operation.OccurredAt) {
			conflict := &SyncConflict{Version: version, OperationID: operation.OperationID,
				Reason: "server_record_is_newer", Current: current}
			store.events = append(store.events, memorySyncEvent{userID: userID, version: version,
				payload: syncEventPayload{Conflict: conflict}})
			continue
		}

		if operation.Type == "delete" {
			store.deleteOperationRecordsLocked(userID, operation)
			if current == nil {
				copy := recordFromOperation(userID, operation)
				current = &copy
			}
			change := &SyncChange{Version: version, OperationID: operation.OperationID, Type: "delete", Record: current}
			store.events = append(store.events, memorySyncEvent{userID: userID, version: version,
				payload: syncEventPayload{Change: change}})
			continue
		}

		record := recordFromOperation(userID, operation)
		if currentIndex >= 0 {
			record.ID = store.records[currentIndex].ID
			store.records[currentIndex] = record
		} else {
			store.upsertLocked(record)
		}
		index := store.findOperationRecordLocked(userID, operation)
		if index >= 0 {
			copy := store.records[index]
			record = copy
		}
		change := &SyncChange{Version: version, OperationID: operation.OperationID, Type: operation.Type, Record: &record}
		store.events = append(store.events, memorySyncEvent{userID: userID, version: version,
			payload: syncEventPayload{Change: change}})
	}
	return store.syncResultLocked(userID, request.Cursor), nil
}

func (store *MemoryStore) bootstrapSyncEventsLocked(userID int) {
	for index := range store.records {
		record := store.records[index]
		if record.UserID != userID {
			continue
		}
		represented := false
		for _, event := range store.events {
			if event.userID == userID && event.payload.Change != nil && event.payload.Change.Record != nil &&
				event.payload.Change.Record.ID == record.ID {
				represented = true
				break
			}
		}
		if represented {
			continue
		}
		operationID := "bootstrap-history-" + itoa(record.ID)
		operationKey := syncOperationKey(userID, "server-bootstrap", operationID)
		if _, exists := store.operations[operationKey]; exists {
			continue
		}
		version := store.nextVersion
		store.nextVersion++
		store.operations[operationKey] = version
		copy := record
		store.events = append(store.events, memorySyncEvent{userID: userID, version: version,
			payload: syncEventPayload{Change: &SyncChange{Version: version, OperationID: operationID, Type: "upsert", Record: &copy}}})
	}
}

func (store *MemoryStore) upsertLocked(record Record) {
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.WatchedAt
	}
	if record.SeasonNumber < 1 {
		record.SeasonNumber = 1
	}
	if record.EntryPage != "watch" {
		record.EntryPage = "play"
	}
	for index := range store.records {
		existing := &store.records[index]
		if existing.UserID == record.UserID && existing.Source == record.Source && existing.VodID == record.VodID {
			record.ID = existing.ID
			store.records[index] = record
			return
		}
	}
	record.ID = store.nextID
	store.nextID++
	store.records = append(store.records, record)
}

func (store *MemoryStore) findOperationRecordLocked(userID int, operation SyncOperation) int {
	match := -1
	for index := range store.records {
		record := store.records[index]
		if record.UserID != userID || !operationMatchesRecord(operation, record) {
			continue
		}
		if match < 0 || recordTime(record).After(recordTime(store.records[match])) {
			match = index
		}
	}
	return match
}

func (store *MemoryStore) deleteOperationRecordsLocked(userID int, operation SyncOperation) {
	kept := store.records[:0]
	for _, record := range store.records {
		if record.UserID == userID && operationMatchesRecord(operation, record) {
			continue
		}
		kept = append(kept, record)
	}
	store.records = kept
}

func (store *MemoryStore) syncResultLocked(userID int, cursor int64) SyncV2Result {
	maxVersion := int64(0)
	for _, event := range store.events {
		if event.userID == userID && event.version > maxVersion {
			maxVersion = event.version
		}
	}
	requiresFullSync := cursor > maxVersion
	if requiresFullSync {
		cursor = 0
	}
	result := SyncV2Result{
		Cursor:           cursor,
		Changes:          []SyncChange{},
		Conflicts:        []SyncConflict{},
		RequiresFullSync: requiresFullSync,
	}
	count := 0
	for _, event := range store.events {
		if event.userID != userID || event.version <= cursor {
			continue
		}
		if count == maxSyncChanges {
			break
		}
		result.Cursor = event.version
		if event.payload.Change != nil {
			result.Changes = append(result.Changes, *event.payload.Change)
		}
		if event.payload.Conflict != nil {
			result.Conflicts = append(result.Conflicts, *event.payload.Conflict)
		}
		count++
	}
	return result
}

func syncOperationKey(userID int, deviceID, operationID string) string {
	return itoa(userID) + "\x00" + deviceID + "\x00" + operationID
}
