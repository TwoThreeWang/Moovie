package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

func (store *PostgresStore) SyncV2(ctx context.Context, userID int, request SyncV2Request, _ time.Time) (SyncV2Result, error) {
	executor := store.database
	var transaction database.Transaction
	if store.beginner != nil {
		var err error
		transaction, err = store.beginner.Begin(ctx)
		if err != nil {
			return SyncV2Result{}, fmt.Errorf("begin history sync: %w", err)
		}
		defer transaction.Rollback(context.WithoutCancel(ctx))
		executor = transaction
	}
	if err := bootstrapSyncEvents(ctx, executor, userID); err != nil {
		return SyncV2Result{}, err
	}

	for _, operation := range request.Operations {
		version, reserved, err := reserveSyncOperation(ctx, executor, userID, request.DeviceID, operation)
		if err != nil {
			return SyncV2Result{}, err
		}
		if !reserved {
			continue
		}
		payload, err := applySyncOperation(ctx, executor, userID, version, operation)
		if err != nil {
			return SyncV2Result{}, err
		}
		if err := finalizeSyncEvent(ctx, executor, userID, version, operation, payload); err != nil {
			return SyncV2Result{}, err
		}
	}

	result, err := listSyncResult(ctx, executor, userID, request.Cursor)
	if err != nil {
		return SyncV2Result{}, err
	}
	if transaction != nil {
		if err := transaction.Commit(ctx); err != nil {
			return SyncV2Result{}, fmt.Errorf("commit history sync: %w", err)
		}
	}
	return result, nil
}

func bootstrapSyncEvents(ctx context.Context, executor database.Executor, userID int) error {
	records, err := listSyncRecords(ctx, executor, userID)
	if err != nil {
		return err
	}
	for index := range records {
		record := records[index]
		operationType := "upsert"
		if record.Progress >= 100 {
			operationType = "complete"
		}
		operation := SyncOperation{
			OperationID: "bootstrap-position-" + itoa(record.ID), Type: operationType, HistoryID: record.ID,
			MediaID: record.MediaID, MediaUnitID: record.MediaUnitID, DoubanID: record.DoubanID, VodID: record.VodID, Source: record.Source,
			Title: record.Title, Poster: record.Poster, Episode: record.Episode, Season: record.SeasonNumber,
			EpisodeKey: record.EpisodeKey, Position: record.LastTime, Duration: record.Duration,
			Progress: record.Progress, EntryPage: record.EntryPage, OccurredAt: recordTime(record),
		}
		version, reserved, err := reserveSyncOperation(ctx, executor, userID, "server-bootstrap", operation)
		if err != nil {
			return err
		}
		if !reserved {
			continue
		}
		copy := record
		payload := syncEventPayload{Change: &SyncChange{Version: version, OperationID: operation.OperationID,
			Type: "upsert", Record: &copy}}
		if err := finalizeSyncEvent(ctx, executor, userID, version, operation, payload); err != nil {
			return err
		}
	}
	return nil
}

func finalizeSyncEvent(ctx context.Context, executor database.Executor, userID int, version int64, operation SyncOperation, payload syncEventPayload) error {
	var current *Record
	if payload.Change != nil {
		current = payload.Change.Record
	} else if payload.Conflict != nil {
		current = payload.Conflict.Current
	}
	if current != nil {
		operation.HistoryID = current.ID
		operation.MediaID = current.MediaID
		operation.MediaUnitID = current.MediaUnitID
		operation.Season = current.SeasonNumber
		operation.EpisodeKey = current.EpisodeKey
		operation.Source = current.Source
		operation.VodID = current.VodID
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode history sync event: %w", err)
	}
	conflictReason := ""
	if payload.Conflict != nil {
		conflictReason = payload.Conflict.Reason
	}
	if _, err := executor.Exec(ctx, `UPDATE history_sync_events SET payload_json = $2::jsonb,
conflict_reason = $3, record_id = $4, media_id = $5, media_unit_id = $6, season_number = $7,
episode_key = $8, source_key = $9, vod_id = $10 WHERE version = $1 AND user_id = $11`,
		version, string(encoded), conflictReason, nullablePositive(operation.HistoryID), nullablePositive(operation.MediaID),
		nullablePositive(operation.MediaUnitID), operation.Season, operation.EpisodeKey, operation.Source, operation.VodID, userID); err != nil {
		return fmt.Errorf("finalize history sync event: %w", err)
	}
	return nil
}

func listSyncRecords(ctx context.Context, executor database.Executor, userID int) ([]Record, error) {
	return queryPlaybackPositions(ctx, executor, `position.user_id = $1 AND position.deleted_at IS NULL
AND NOT EXISTS (SELECT 1 FROM history_sync_events event WHERE event.user_id = position.user_id
  AND event.record_id = position.id AND event.conflict_reason = '')`, userID)
}

func reserveSyncOperation(ctx context.Context, executor database.Executor, userID int, deviceID string, operation SyncOperation) (int64, bool, error) {
	// 先查后插：BIGSERIAL 的 version 在 ON CONFLICT DO NOTHING 命中时也会自增，
	// 幂等重试会在只追加账本里烧出空洞游标。
	err := executor.QueryRow(ctx, `SELECT version FROM history_sync_events
WHERE user_id = $1 AND device_id = $2 AND operation_id = $3`, userID, deviceID, operation.OperationID).Scan(new(int64))
	if err == nil {
		return 0, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("check history sync operation: %w", err)
	}
	var version int64
	err = executor.QueryRow(ctx, `INSERT INTO history_sync_events
(user_id, device_id, operation_id, device_seq, operation_type, record_id, media_id, media_unit_id,
 season_number, episode_key, source_key, vod_id, occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (user_id, device_id, operation_id) DO NOTHING
	RETURNING version`, userID, deviceID, operation.OperationID, operation.DeviceSeq, operation.Type,
		nullablePositive(operation.HistoryID), nullablePositive(operation.MediaID), nullablePositive(operation.MediaUnitID),
		operation.Season, operation.EpisodeKey, operation.Source, operation.VodID, operation.OccurredAt).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("reserve history sync operation: %w", err)
	}
	return version, true, nil
}

func applySyncOperation(ctx context.Context, executor database.Executor, userID int, version int64, operation SyncOperation) (syncEventPayload, error) {
	current, err := findOperationRecord(ctx, executor, userID, operation, true)
	if err != nil {
		return syncEventPayload{}, err
	}
	if current != nil && !operation.force && recordTime(*current).After(operation.OccurredAt) {
		return syncEventPayload{Conflict: &SyncConflict{Version: version, OperationID: operation.OperationID,
			Reason: "server_record_is_newer", Current: current}}, nil
	}
	if operation.Type == "delete" {
		if current != nil {
			operation.HistoryID, operation.MediaID, operation.MediaUnitID = current.ID, current.MediaID, current.MediaUnitID
			operation.DoubanID, operation.Source, operation.VodID = current.DoubanID, current.Source, current.VodID
			operation.Title, operation.Poster, operation.Episode = current.Title, current.Poster, current.Episode
			operation.Season, operation.EpisodeKey = current.SeasonNumber, current.EpisodeKey
			operation.Position, operation.Duration, operation.Progress = current.LastTime, current.Duration, current.Progress
		}
		if current != nil || operation.Source != "" || operation.MediaID > 0 || operation.DoubanID != "" {
			if err := upsertPlaybackPosition(ctx, executor, userID, operation); err != nil {
				return syncEventPayload{}, err
			}
			current, err = findOperationRecord(ctx, executor, userID, operation, false)
			if err != nil {
				return syncEventPayload{}, err
			}
		}
		if current == nil {
			record := recordFromOperation(userID, operation)
			current = &record
		}
		return syncEventPayload{Change: &SyncChange{Version: version, OperationID: operation.OperationID,
			Type: "delete", Record: current}}, nil
	}

	record := recordFromOperation(userID, operation)
	if err := upsertRecord(ctx, executor, record); err != nil {
		return syncEventPayload{}, err
	}
	current, err = findOperationRecord(ctx, executor, userID, operation, false)
	if err != nil {
		return syncEventPayload{}, err
	}
	if current == nil {
		current = &record
	}
	return syncEventPayload{Change: &SyncChange{Version: version, OperationID: operation.OperationID,
		Type: operation.Type, Record: current}}, nil
}

func findOperationRecord(ctx context.Context, executor database.Executor, userID int, operation SyncOperation, lock bool) (*Record, error) {
	predicate := `position.user_id = $1 AND (
($2 > 0 AND position.id = $2) OR
($3 > 0 AND position.media_unit_id = $3) OR
($4 > 0 AND position.media_id = $4 AND position.season_number = $5 AND position.episode_key = $6) OR
($7 <> '' AND position.last_source_key = $7 AND position.last_vod_id = $8))
ORDER BY position.activity_at DESC LIMIT 1`
	if lock {
		predicate += ` FOR UPDATE OF position`
	}
	records, err := queryPlaybackPositions(ctx, executor, predicate, userID, operation.HistoryID, operation.MediaUnitID, operation.MediaID,
		operation.Season, operation.EpisodeKey, operation.Source, operation.VodID)
	if err != nil {
		return nil, fmt.Errorf("find synchronized playback position: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	return &records[0], nil
}

func listSyncResult(ctx context.Context, executor database.Executor, userID int, cursor int64) (SyncV2Result, error) {
	var maxVersion int64
	if err := executor.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM history_sync_events
WHERE user_id = $1`, userID).Scan(&maxVersion); err != nil {
		return SyncV2Result{}, fmt.Errorf("find latest history sync cursor: %w", err)
	}
	requiresFullSync := cursor > maxVersion
	if requiresFullSync {
		cursor = 0
	}
	rows, err := executor.Query(ctx, `SELECT version, payload_json FROM history_sync_events
WHERE user_id = $1 AND version > $2 ORDER BY version ASC LIMIT $3`, userID, cursor, maxSyncChanges)
	if err != nil {
		return SyncV2Result{}, fmt.Errorf("list history sync events: %w", err)
	}
	defer rows.Close()
	result := SyncV2Result{
		Cursor:           cursor,
		Changes:          []SyncChange{},
		Conflicts:        []SyncConflict{},
		RequiresFullSync: requiresFullSync,
	}
	for rows.Next() {
		var version int64
		var encoded []byte
		if err := rows.Scan(&version, &encoded); err != nil {
			return SyncV2Result{}, fmt.Errorf("scan history sync event: %w", err)
		}
		var payload syncEventPayload
		if err := json.Unmarshal(encoded, &payload); err != nil {
			return SyncV2Result{}, fmt.Errorf("decode history sync event %d: %w", version, err)
		}
		result.Cursor = version
		if payload.Change != nil {
			payload.Change.Version = version
			result.Changes = append(result.Changes, *payload.Change)
		}
		if payload.Conflict != nil {
			payload.Conflict.Version = version
			result.Conflicts = append(result.Conflicts, *payload.Conflict)
		}
	}
	if err := rows.Err(); err != nil {
		return SyncV2Result{}, fmt.Errorf("iterate history sync events: %w", err)
	}
	return result, nil
}

func nullablePositive(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}
