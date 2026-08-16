package mediaidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidPlaybackEvent = errors.New("invalid playback attempt event")

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
	reasonClass := playbackFailureClass(event.Reason)
	updated, err := store.database.Exec(ctx, `WITH inserted AS (
    INSERT INTO playback_attempt_events
    (attempt_id, candidate_session_id, event_type, candidate_id, play_line_id, media_unit_id, media_id,
     source_key, vod_id, elapsed_ms, failure_reason, created_at)
    SELECT $1, $10, $2, candidate.id, candidate.line_id, candidate.media_unit_id, candidate.media_id,
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
), rolled_up AS (
INSERT INTO playback_quality_rollups
(bucket, candidate_id, play_line_id, media_unit_id, media_id, attempt_count,
 manifest_count, first_frame_count, first_frame_total_ms, success_count,
 failure_count, rebuffer_count, switched_count, ended_count, abandoned_count,
 timeout_count, network_count, decode_count, http_count, unknown_failure_count,
 last_success_at, last_failure_at, updated_at)
SELECT date_trunc('hour', created_at), candidate_id, play_line_id, media_unit_id, media_id,
       CASE WHEN event_type = 'attempt_started' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'manifest_loaded' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'first_frame' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'first_frame' THEN elapsed_ms ELSE 0 END,
       CASE WHEN event_type = 'played_10s' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'fatal_error' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'rebuffer' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'source_switched' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'ended' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'abandoned' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'fatal_error' AND $9 = 'timeout' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'fatal_error' AND $9 = 'network' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'fatal_error' AND $9 = 'decode' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'fatal_error' AND $9 = 'http' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'fatal_error' AND $9 = 'unknown' THEN 1 ELSE 0 END,
       CASE WHEN event_type = 'played_10s' THEN created_at ELSE NULL END,
       CASE WHEN event_type = 'fatal_error' THEN created_at ELSE NULL END,
       NOW()
FROM inserted
ON CONFLICT (bucket, candidate_id) DO UPDATE SET
attempt_count = playback_quality_rollups.attempt_count + EXCLUDED.attempt_count,
manifest_count = playback_quality_rollups.manifest_count + EXCLUDED.manifest_count,
first_frame_count = playback_quality_rollups.first_frame_count + EXCLUDED.first_frame_count,
first_frame_total_ms = playback_quality_rollups.first_frame_total_ms + EXCLUDED.first_frame_total_ms,
success_count = playback_quality_rollups.success_count + EXCLUDED.success_count,
failure_count = playback_quality_rollups.failure_count + EXCLUDED.failure_count,
rebuffer_count = playback_quality_rollups.rebuffer_count + EXCLUDED.rebuffer_count,
switched_count = playback_quality_rollups.switched_count + EXCLUDED.switched_count,
ended_count = playback_quality_rollups.ended_count + EXCLUDED.ended_count,
abandoned_count = playback_quality_rollups.abandoned_count + EXCLUDED.abandoned_count,
timeout_count = playback_quality_rollups.timeout_count + EXCLUDED.timeout_count,
network_count = playback_quality_rollups.network_count + EXCLUDED.network_count,
decode_count = playback_quality_rollups.decode_count + EXCLUDED.decode_count,
http_count = playback_quality_rollups.http_count + EXCLUDED.http_count,
unknown_failure_count = playback_quality_rollups.unknown_failure_count + EXCLUDED.unknown_failure_count,
last_success_at = COALESCE(EXCLUDED.last_success_at, playback_quality_rollups.last_success_at),
last_failure_at = COALESCE(EXCLUDED.last_failure_at, playback_quality_rollups.last_failure_at),
updated_at = NOW()
RETURNING candidate_id
)
UPDATE vod_items resource
SET last_accessed_at = inserted.created_at,
    last_played_at = CASE WHEN inserted.event_type IN ('played_10s', 'ended')
                          THEN inserted.created_at ELSE resource.last_played_at END,
    last_success_at = CASE WHEN inserted.event_type = 'played_10s'
                           THEN inserted.created_at ELSE resource.last_success_at END,
    resource_status = CASE WHEN resource.resource_status = 'cold' THEN 'active' ELSE resource.resource_status END,
    cold_at = CASE WHEN resource.resource_status = 'cold' THEN NULL ELSE resource.cold_at END,
    lifecycle_batch_id = CASE WHEN resource.resource_status = 'cold' THEN NULL ELSE resource.lifecycle_batch_id END,
    updated_at = NOW()
FROM inserted, rolled_up
WHERE resource.source_key = inserted.source_key AND resource.vod_id = inserted.vod_id
  AND rolled_up.candidate_id = inserted.candidate_id`, event.AttemptID, event.EventType, event.CandidateID, event.MediaUnitID,
		event.SourceKey, event.VodID, event.ElapsedMs, event.Reason, reasonClass, event.CandidateSessionID)
	if err != nil {
		return false, fmt.Errorf("record playback attempt event: %w", err)
	}
	return updated > 0, nil
}

func playbackFailureClass(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(reason, "timeout") || strings.Contains(reason, "超时"):
		return "timeout"
	case strings.Contains(reason, "network") || strings.Contains(reason, "manifest") || strings.Contains(reason, "fetch"):
		return "network"
	case strings.Contains(reason, "decode") || strings.Contains(reason, "codec") || strings.Contains(reason, "media"):
		return "decode"
	case strings.Contains(reason, "http") || strings.Contains(reason, "403") || strings.Contains(reason, "404") || strings.Contains(reason, "5xx"):
		return "http"
	default:
		return "unknown"
	}
}
