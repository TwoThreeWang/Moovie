package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type ResourceCoolingPreview struct {
	BatchID            int64            `json:"batch_id"`
	Eligible           int              `json:"eligible"`
	SourceDistribution map[string]int   `json:"source_distribution"`
	StatusDistribution map[string]int   `json:"status_distribution"`
	HistoryReferences  int              `json:"history_references"`
	UniqueResources    int              `json:"unique_resources"`
	EstimatedBytes     int64            `json:"estimated_bytes"`
	Samples            []map[string]any `json:"samples"`
	ExpiresAt          time.Time        `json:"expires_at"`
}

// ResourceCoolingStore 只支持可恢复的状态转换。Preview 会冻结精确候选集合，
// Apply 必须引用尚未过期的同一批次。
type ResourceCoolingStore interface {
	PreviewCooling(ctx context.Context, days int) (ResourceCoolingPreview, error)
	ApplyCooling(ctx context.Context, batchID int64, confirmed bool) (int, error)
	RestoreCold(ctx context.Context, sourceKey, vodID string) (int, error)
}

func (store *PostgresStore) PreviewCooling(ctx context.Context, days int) (ResourceCoolingPreview, error) {
	if days < 30 {
		return ResourceCoolingPreview{}, fmt.Errorf("cooling observation window must be at least 30 days")
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	discoveryCutoff := time.Now().AddDate(0, 0, -14)
	expiresAt := time.Now().Add(24 * time.Hour)
	var preview ResourceCoolingPreview
	var sourceJSON, statusJSON, sampleJSON []byte
	err := store.database.QueryRow(ctx, `WITH classified AS (
    SELECT v.source_key, v.vod_id, COALESCE(v.resource_status, 'active') AS previous_status,
                   EXISTS (
                       SELECT 1 FROM playback_positions position
                       WHERE position.deleted_at IS NULL
                         AND position.last_source_key = v.source_key AND position.last_vod_id = v.vod_id
           ) AS has_history,
           CASE WHEN COALESCE(v.media_id, link.media_id) IS NULL THEN FALSE ELSE NOT EXISTS (
               SELECT 1
               FROM resource_media_links alternative_link
               JOIN vod_items alternative
                 ON alternative.source_key = alternative_link.source_key AND alternative.vod_id = alternative_link.vod_id
               WHERE alternative_link.media_id = COALESCE(v.media_id, link.media_id)
                 AND (alternative.source_key, alternative.vod_id) <> (v.source_key, v.vod_id)
                 AND COALESCE(alternative.resource_status, 'active') IN ('active', 'stale', 'cold')
           ) END AS is_unique,
           OCTET_LENGTH(COALESCE(v.vod_content, '')) + OCTET_LENGTH(COALESCE(v.vod_play_url, '')) AS estimated_bytes
    FROM vod_items v
    LEFT JOIN resource_media_links link ON link.source_key = v.source_key AND link.vod_id = v.vod_id
    WHERE COALESCE(v.resource_status, 'active') IN ('active', 'stale')
      AND COALESCE(v.last_played_at, TO_TIMESTAMP(0)) < $1
      AND COALESCE(v.last_success_at, TO_TIMESTAMP(0)) < $1
      AND COALESCE(v.last_discovered_at, v.last_seen_at, v.last_visited_at) < $2
), eligible AS (
    SELECT * FROM classified WHERE has_history = FALSE AND is_unique = FALSE
), batch AS (
    INSERT INTO resource_lifecycle_batches
    (action, status, cutoff_at, candidate_count, source_distribution, status_distribution,
     history_reference_count, unique_resource_count, sample_records, estimated_bytes, created_at, expires_at)
    SELECT 'cool', 'previewed', $1, COUNT(*)::integer,
           COALESCE((SELECT JSONB_OBJECT_AGG(source_key, source_count) FROM
               (SELECT source_key, COUNT(*) AS source_count FROM eligible GROUP BY source_key) source_counts), '{}'::jsonb),
           COALESCE((SELECT JSONB_OBJECT_AGG(previous_status, status_count) FROM
               (SELECT previous_status, COUNT(*) AS status_count FROM eligible GROUP BY previous_status) status_counts), '{}'::jsonb),
           (SELECT COUNT(*)::integer FROM classified WHERE has_history),
           (SELECT COUNT(*)::integer FROM classified WHERE is_unique),
           COALESCE((SELECT JSONB_AGG(JSONB_BUILD_OBJECT('source_key', source_key, 'vod_id', vod_id, 'status', previous_status))
               FROM (SELECT source_key, vod_id, previous_status FROM eligible ORDER BY source_key, vod_id LIMIT 20) sample), '[]'::jsonb),
           COALESCE(SUM(estimated_bytes), 0)::bigint, NOW(), $3
    FROM eligible
    RETURNING id, candidate_count, source_distribution, status_distribution,
              history_reference_count, unique_resource_count, sample_records, estimated_bytes, expires_at
), items AS (
    INSERT INTO resource_lifecycle_batch_items (batch_id, source_key, vod_id, previous_status)
    SELECT batch.id, eligible.source_key, eligible.vod_id, eligible.previous_status
    FROM batch CROSS JOIN eligible
    RETURNING batch_id
)
SELECT id, candidate_count, source_distribution, status_distribution,
       history_reference_count, unique_resource_count, sample_records, estimated_bytes, expires_at
FROM batch`, cutoff, discoveryCutoff, expiresAt).Scan(
		&preview.BatchID, &preview.Eligible, &sourceJSON, &statusJSON,
		&preview.HistoryReferences, &preview.UniqueResources, &sampleJSON, &preview.EstimatedBytes, &preview.ExpiresAt,
	)
	if err != nil {
		return ResourceCoolingPreview{}, fmt.Errorf("preview resource cooling: %w", err)
	}
	if err := json.Unmarshal(sourceJSON, &preview.SourceDistribution); err != nil {
		return ResourceCoolingPreview{}, fmt.Errorf("decode cooling source distribution: %w", err)
	}
	if err := json.Unmarshal(statusJSON, &preview.StatusDistribution); err != nil {
		return ResourceCoolingPreview{}, fmt.Errorf("decode cooling status distribution: %w", err)
	}
	if err := json.Unmarshal(sampleJSON, &preview.Samples); err != nil {
		return ResourceCoolingPreview{}, fmt.Errorf("decode cooling samples: %w", err)
	}
	return preview, nil
}

func (store *PostgresStore) ApplyCooling(ctx context.Context, batchID int64, confirmed bool) (int, error) {
	if !confirmed {
		return 0, errors.New("resource cooling requires explicit confirmation")
	}
	if batchID <= 0 {
		return 0, errors.New("a valid dry-run batch id is required")
	}
	var applied int
	err := store.database.QueryRow(ctx, `WITH valid_batch AS (
	SELECT id FROM resource_lifecycle_batches
	WHERE id = $1 AND action = 'cool' AND status = 'previewed' AND expires_at > NOW()
	FOR UPDATE
), cooled AS (
	UPDATE vod_items resource
	SET resource_status = 'cold', cold_at = COALESCE(resource.cold_at, NOW()),
	    lifecycle_batch_id = valid_batch.id, updated_at = NOW()
	FROM resource_lifecycle_batch_items item, valid_batch
	WHERE item.batch_id = valid_batch.id
	  AND resource.source_key = item.source_key AND resource.vod_id = item.vod_id
	  AND COALESCE(resource.resource_status, 'active') = item.previous_status
	RETURNING valid_batch.id
)
UPDATE resource_lifecycle_batches batch
SET status = 'applied', applied_at = NOW(), applied_count = (SELECT COUNT(*) FROM cooled)
FROM valid_batch
WHERE batch.id = valid_batch.id
RETURNING batch.applied_count`, batchID).Scan(&applied)
	if err != nil {
		return 0, fmt.Errorf("apply resource cooling batch: %w", err)
	}
	return applied, nil
}

func (store *PostgresStore) RestoreCold(ctx context.Context, sourceKey, vodID string) (int, error) {
	if sourceKey == "" || vodID == "" {
		return 0, errors.New("source key and vod id are required")
	}
	affected, err := store.database.Exec(ctx, `UPDATE vod_items
SET resource_status = 'active', stale_at = NULL, retired_at = NULL, cold_at = NULL,
    lifecycle_batch_id = NULL, updated_at = NOW()
WHERE source_key = $1 AND vod_id = $2 AND resource_status = 'cold'`, sourceKey, vodID)
	if err != nil {
		return 0, fmt.Errorf("restore cold resource: %w", err)
	}
	return int(affected), nil
}

type memoryCoolingBatch struct {
	items     [][2]string
	expiresAt time.Time
	applied   bool
}

func (store *MemoryStore) PreviewCooling(_ context.Context, days int) (ResourceCoolingPreview, error) {
	if days < 30 {
		return ResourceCoolingPreview{}, fmt.Errorf("cooling observation window must be at least 30 days")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -days)
	items := make([][2]string, 0)
	sources := make(map[string]int)
	statuses := make(map[string]int)
	samples := make([]map[string]any, 0)
	for _, item := range store.items {
		if item.ResourceStatus != "stale" || !item.LastVisitedAt.Before(cutoff) {
			continue
		}
		items = append(items, [2]string{item.SourceKey, item.VodId})
		sources[item.SourceKey]++
		statuses[item.ResourceStatus]++
		if len(samples) < 20 {
			samples = append(samples, map[string]any{"source_key": item.SourceKey, "vod_id": item.VodId, "status": item.ResourceStatus})
		}
	}
	batchID := store.nextCoolingBatchID
	store.nextCoolingBatchID++
	expiresAt := time.Now().Add(24 * time.Hour)
	store.coolingBatches[batchID] = memoryCoolingBatch{items: items, expiresAt: expiresAt}
	return ResourceCoolingPreview{BatchID: batchID, Eligible: len(items), SourceDistribution: sources,
		StatusDistribution: statuses, Samples: samples, ExpiresAt: expiresAt}, nil
}

func (store *MemoryStore) ApplyCooling(_ context.Context, batchID int64, confirmed bool) (int, error) {
	if !confirmed {
		return 0, errors.New("resource cooling requires explicit confirmation")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	batch, found := store.coolingBatches[batchID]
	if !found || batch.applied || time.Now().After(batch.expiresAt) {
		return 0, errors.New("dry-run batch is missing, expired, or already applied")
	}
	affected := 0
	for _, key := range batch.items {
		for index := range store.items {
			item := &store.items[index]
			if item.SourceKey == key[0] && item.VodId == key[1] && item.ResourceStatus == "stale" {
				item.ResourceStatus = "cold"
				affected++
			}
		}
	}
	batch.applied = true
	store.coolingBatches[batchID] = batch
	return affected, nil
}

func (store *MemoryStore) RestoreCold(_ context.Context, sourceKey, vodID string) (int, error) {
	if sourceKey == "" || vodID == "" {
		return 0, errors.New("source key and vod id are required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.items {
		item := &store.items[index]
		if item.SourceKey == sourceKey && item.VodId == vodID && item.ResourceStatus == "cold" {
			item.ResourceStatus = "active"
			return 1, nil
		}
	}
	return 0, nil
}
