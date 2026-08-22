package danmaku

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// PostgresStore 是弹幕的 PostgreSQL 实现，只涉及 danmaku 一张表。
type PostgresStore struct{ database database.Beginner }

// NewPostgresStore 创建弹幕存储。
func NewPostgresStore(beginner database.Beginner) *PostgresStore {
	return &PostgresStore{database: beginner}
}

// recordColumns 是各查询共用的字段列表。
const recordColumns = `id, vod_key, time, text, mode, color, user_id, deleted, created_at`

// ListByVodKey 读某一集的弹幕，已软删除的不返回。
func (store *PostgresStore) ListByVodKey(ctx context.Context, vodKey string, limit int) ([]Record, error) {
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin danmaku read: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))
	rows, err := transaction.Query(ctx, `SELECT `+recordColumns+` FROM danmakus
WHERE vod_key = $1 AND deleted = FALSE ORDER BY time ASC LIMIT $2`, vodKey, limit)
	if err != nil {
		return nil, fmt.Errorf("list danmaku: %w", err)
	}
	defer rows.Close()
	return scanRecords(rows)
}

// CreateGuarded 在一个事务里同时做频率检查、重复检查和写入，
// 放在事务里是为了并发提交时也拦得住。
func (store *PostgresStore) CreateGuarded(ctx context.Context, record Record, rateSince, duplicateSince time.Time, maxPerWindow int) (*Record, error) {
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin danmaku create: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))
	// 按用户串行化发送，使计数、重复检查和插入即使跨多个应用实例也构成一个原子临界区。
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(record.UserID)); err != nil {
		return nil, fmt.Errorf("lock danmaku sender: %w", err)
	}
	var count int
	if err := transaction.QueryRow(ctx, `SELECT COUNT(*) FROM danmakus WHERE user_id = $1 AND created_at >= $2`, record.UserID, rateSince).Scan(&count); err != nil {
		return nil, fmt.Errorf("count recent danmaku: %w", err)
	}
	if count >= maxPerWindow {
		return nil, ErrRateLimited
	}
	var duplicate bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM danmakus
WHERE user_id = $1 AND vod_key = $2 AND text = $3 AND created_at >= $4)`, record.UserID, record.VodKey, record.Text, duplicateSince).Scan(&duplicate); err != nil {
		return nil, fmt.Errorf("check duplicate danmaku: %w", err)
	}
	if duplicate {
		return nil, ErrDuplicate
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if err := transaction.QueryRow(ctx, `INSERT INTO danmakus
(vod_key, time, text, mode, color, user_id, deleted, created_at)
VALUES ($1,$2,$3,$4,$5,$6,FALSE,$7) RETURNING id`, record.VodKey, record.Time, record.Text,
		record.Mode, record.Color, record.UserID, record.CreatedAt).Scan(&record.ID); err != nil {
		return nil, fmt.Errorf("create danmaku: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit danmaku create: %w", err)
	}
	return &record, nil
}

// SoftDelete 软删除一条弹幕。
func (store *PostgresStore) SoftDelete(ctx context.Context, id int) error {
	return store.exec(ctx, `UPDATE danmakus SET deleted = TRUE WHERE id = $1`, id)
}

// ListRecent 分页列出最近的弹幕，后台审核用。
func (store *PostgresStore) ListRecent(ctx context.Context, limit, offset int) ([]Record, error) {
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin recent danmaku read: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))
	rows, err := transaction.Query(ctx, `SELECT `+recordColumns+` FROM danmakus
WHERE deleted = FALSE ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list recent danmaku: %w", err)
	}
	defer rows.Close()
	return scanRecords(rows)
}

// exec 执行一条写语句。
func (store *PostgresStore) exec(ctx context.Context, query string, arguments ...any) error {
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))
	if _, err := transaction.Exec(ctx, query, arguments...); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// scanRecords 把查询结果扫成弹幕列表。
func scanRecords(rows database.Rows) ([]Record, error) {
	records := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.VodKey, &record.Time, &record.Text, &record.Mode,
			&record.Color, &record.UserID, &record.Deleted, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan danmaku: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("iterate danmaku: %w", err)
	}
	return records, nil
}
