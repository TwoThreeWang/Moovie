package playback

import (
	"context"
	"fmt"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// AdFingerprintPostgresStore 是广告指纹的 PostgreSQL 实现。
type AdFingerprintPostgresStore struct{ database database.Executor }

// NewAdFingerprintPostgresStore 创建存储实现。
func NewAdFingerprintPostgresStore(executor database.Executor) *AdFingerprintPostgresStore {
	return &AdFingerprintPostgresStore{database: executor}
}

// MatchFingerprints 批量主键精确查询，只返回数据库中已有的记录。
func (store *AdFingerprintPostgresStore) MatchFingerprints(ctx context.Context, fingerprints [][]byte) ([]AdFingerprint, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(fingerprints))
	args := make([]any, len(fingerprints))
	for i, fp := range fingerprints {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = fp
	}
	query := `SELECT fingerprint, confirm_count, reject_count FROM ad_fingerprints WHERE fingerprint IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := store.database.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("match fingerprints: %w", err)
	}
	defer rows.Close()
	var results []AdFingerprint
	for rows.Next() {
		var f AdFingerprint
		if err := rows.Scan(&f.Fingerprint, &f.ConfirmCount, &f.RejectCount); err != nil {
			return nil, fmt.Errorf("scan fingerprint: %w", err)
		}
		results = append(results, f)
	}
	return results, rows.Err()
}

// VoteConfirm 原子累加确认票。
func (store *AdFingerprintPostgresStore) VoteConfirm(ctx context.Context, fingerprint []byte) (*AdFingerprint, error) {
	return store.vote(ctx, fingerprint, "confirm_count")
}

// VoteReject 原子累加否认票。
func (store *AdFingerprintPostgresStore) VoteReject(ctx context.Context, fingerprint []byte) (*AdFingerprint, error) {
	return store.vote(ctx, fingerprint, "reject_count")
}

func (store *AdFingerprintPostgresStore) vote(ctx context.Context, fingerprint []byte, column string) (*AdFingerprint, error) {
	confirmVal, rejectVal := 0, 0
	if column == "confirm_count" {
		confirmVal = 1
	} else {
		rejectVal = 1
	}
	row := store.database.QueryRow(ctx,
		`INSERT INTO ad_fingerprints (fingerprint, confirm_count, reject_count)
		VALUES ($1, $2, $3)
		ON CONFLICT (fingerprint) DO UPDATE
		SET `+column+` = ad_fingerprints.`+column+` + 1
		RETURNING fingerprint, confirm_count, reject_count`,
		fingerprint, confirmVal, rejectVal)
	var f AdFingerprint
	if err := row.Scan(&f.Fingerprint, &f.ConfirmCount, &f.RejectCount); err != nil {
		return nil, fmt.Errorf("vote %s: %w", column, err)
	}
	return &f, nil
}
