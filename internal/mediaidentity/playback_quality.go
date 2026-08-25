package mediaidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidPlaybackEvent 表示上报的播放事件字段不合法，直接丢弃。
var ErrInvalidPlaybackEvent = errors.New("invalid playback attempt event")

const TaskQualityRefresh = "quality_refresh"

// playbackEventTypes 是允许上报的事件类型白名单（这个接口对外开放，必须严格限制取值）。
var playbackEventTypes = map[string]bool{
	"attempt_started": true,
	"manifest_loaded": true,
	"first_frame":     true,
	"played_10s":      true,
	"rebuffer":        true,
	"fatal_error":     true,
	"source_switched": true,
	"ended":           true,
	"abandoned":       true,
}

// RecordPlaybackEvent 记录一次播放事件，更新资源访问时间，并在质量统计过期时投一个重算任务。
// (attempt_id, event_type) 唯一，重复上报会被忽略；返回值表示是否真的写入了新记录。
func (store *PostgresStore) RecordPlaybackEvent(ctx context.Context, event PlaybackAttemptEvent) (bool, error) {
	event.AttemptID = strings.TrimSpace(event.AttemptID)
	event.CandidateSessionID = strings.TrimSpace(event.CandidateSessionID)
	event.EventType = strings.ToLower(strings.TrimSpace(event.EventType))
	event.SourceKey = strings.TrimSpace(event.SourceKey)
	event.VodID = strings.TrimSpace(event.VodID)
	event.Reason = strings.ToLower(strings.TrimSpace(event.Reason))
	if reasonRunes := []rune(event.Reason); len(reasonRunes) > 256 {
		event.Reason = string(reasonRunes[:256])
	}
	if len(event.AttemptID) < 8 || len(event.AttemptID) > 128 || len(event.CandidateSessionID) > 128 ||
		(event.CandidateSessionID != "" && len(event.CandidateSessionID) < 8) || !playbackEventTypes[event.EventType] ||
		event.CandidateID <= 0 || event.MediaUnitID <= 0 || event.SourceKey == "" || event.VodID == "" {
		return false, ErrInvalidPlaybackEvent
	}
	if event.ElapsedMs < 0 {
		event.ElapsedMs = 0
	}
	if event.ElapsedMs > 24*60*60*1000 {
		event.ElapsedMs = 24 * 60 * 60 * 1000
	}
	updated, err := store.database.Exec(ctx, `WITH inserted AS (
    INSERT INTO playback_attempt_events
    (attempt_id, candidate_session_id, event_type, candidate_id, play_line_id, media_unit_id, media_id,
     source_key, vod_id, elapsed_ms, failure_reason, created_at)
    SELECT $1, $9, $2, candidate.id, candidate.line_id, candidate.media_unit_id, candidate.media_id,
           line.source_key, line.vod_id, $7, $8, NOW()
    FROM resource_episode_candidates candidate
    JOIN resource_play_lines line ON line.id = candidate.line_id
    WHERE candidate.id = $3 AND candidate.media_unit_id = $4
      AND line.source_key = $5 AND line.vod_id = $6
      AND candidate.resource_status NOT IN ('retired', 'deleted')
      AND line.resource_status NOT IN ('retired', 'deleted')
    ON CONFLICT (attempt_id, event_type) DO NOTHING
    RETURNING candidate_id, play_line_id, media_unit_id, media_id, source_key, vod_id, candidate_session_id,
              event_type, elapsed_ms, failure_reason, created_at
), enqueued AS (
    INSERT INTO worker_jobs (task_type, subject_key, payload, reason, max_attempts, available_at, priority)
    SELECT 'quality_refresh', inserted.source_key || ':' || inserted.vod_id,
           JSONB_BUILD_OBJECT('source_key', inserted.source_key, 'vod_id', inserted.vod_id),
           'playback_event', 3, NOW(), 5
    FROM inserted
    JOIN vod_items resource ON resource.source_key = inserted.source_key AND resource.vod_id = inserted.vod_id
    WHERE resource.quality_refreshed_at IS NULL OR resource.quality_refreshed_at < NOW() - INTERVAL '1 hour'
    ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO NOTHING
    RETURNING 1
)
UPDATE vod_items resource
SET last_accessed_at = inserted.created_at,
    last_played_at = CASE WHEN inserted.event_type IN ('played_10s', 'ended')
                          THEN inserted.created_at ELSE resource.last_played_at END,
    last_success_at = CASE WHEN inserted.event_type = 'played_10s'
                           THEN inserted.created_at ELSE resource.last_success_at END,
    updated_at = NOW()
FROM inserted
WHERE resource.source_key = inserted.source_key AND resource.vod_id = inserted.vod_id`,
		event.AttemptID, event.EventType, event.CandidateID, event.MediaUnitID,
		event.SourceKey, event.VodID, event.ElapsedMs, event.Reason, event.CandidateSessionID)
	if err != nil {
		return false, fmt.Errorf("record playback attempt event: %w", err)
	}
	return updated > 0, nil
}

// RefreshQuality 从 7 天明细窗口重算一条资源的播放质量统计。
func (store *PostgresStore) RefreshQuality(ctx context.Context, sourceKey, vodID string) error {
	_, err := store.database.Exec(ctx, `UPDATE vod_items SET
    avg_speed_ms = COALESCE(q.avg, 0),
    success_count = COALESCE(q.sc, 0),
    failure_count = COALESCE(q.fc, 0),
    quality_refreshed_at = NOW()
FROM (
    SELECT COUNT(*) FILTER (WHERE e.event_type = 'played_10s')::INT AS sc,
           COUNT(*) FILTER (WHERE e.event_type = 'fatal_error')::INT AS fc,
           COALESCE(AVG(e.elapsed_ms) FILTER (WHERE e.event_type = 'first_frame'), 0)::INT AS avg
    FROM playback_attempt_events e
    JOIN resource_episode_candidates c ON c.id = e.candidate_id
    JOIN resource_play_lines line ON line.id = c.line_id
    WHERE line.source_key = $1 AND line.vod_id = $2
      AND e.created_at >= NOW() - INTERVAL '7 days'
) q
WHERE source_key = $1 AND vod_id = $2`, sourceKey, vodID)
	if err != nil {
		return fmt.Errorf("refresh playback quality: %w", err)
	}
	return nil
}
