package history

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// SyncV2 在一个事务里逐条应用客户端操作，直接写 playback_positions。
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

	for _, operation := range request.Operations {
		if _, err := applySyncOperation(ctx, executor, userID, 0, operation); err != nil {
			return SyncV2Result{}, err
		}
	}

	if transaction != nil {
		if err := transaction.Commit(ctx); err != nil {
			return SyncV2Result{}, fmt.Errorf("commit history sync: %w", err)
		}
	}
	return SyncV2Result{Cursor: 0, Changes: []SyncChange{}, Conflicts: []SyncConflict{}}, nil
}


// applySyncOperation 应用一条操作：先锁住并读取服务端当前记录，
// 服务端记录更新就判为冲突不改；否则写库并返回变更。
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
			if _, err := executor.Exec(ctx, `DELETE FROM playback_positions WHERE id = $1`, current.ID); err != nil {
				return syncEventPayload{}, fmt.Errorf("delete playback position: %w", err)
			}
		} else if operation.Source != "" || operation.MediaID > 0 || operation.DoubanID != "" {
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

// findOperationRecord 按四种身份之一定位服务端记录，lock 为 true 时加行锁。
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


// nullablePositive 把非正数转成 NULL 写库。
func nullablePositive(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}
