package mediaidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidPlaybackEvent = errors.New("invalid playback result")

// RecordPlaybackEvent 一次起播只接受一个终态；插入去重和累计在同一 SQL 完成。
// 只修改统计/播放专用时间，不把用户观看伪装成资源站内容更新。
func (store *PostgresStore) RecordPlaybackEvent(ctx context.Context, event PlaybackAttemptEvent) (bool, error) {
	event.AttemptID = strings.TrimSpace(event.AttemptID)
	if len(event.AttemptID) < 8 || len(event.AttemptID) > 128 || event.SourceKey == "" || event.VodID == "" || len(event.PlaybackVersion) != 32 ||
		(event.EventType != "success" && event.EventType != "failure") || event.MediaUnitID < 0 || event.ElapsedMs < 0 || event.ElapsedMs > 120000 ||
		(event.EventType == "success" && event.ElapsedMs == 0) {
		return false, ErrInvalidPlaybackEvent
	}
	success := event.EventType == "success"
	if !success {
		event.ElapsedMs = 0
	}
	count, err := store.database.Exec(ctx, `WITH resource AS MATERIALIZED (
 SELECT * FROM vod_items WHERE source_key=$2 AND vod_id=$3 FOR UPDATE
), inserted AS (
 INSERT INTO playback_results(attempt_id,media_id,media_unit_id,source_key,vod_id,playback_version,succeeded,load_ms)
 SELECT $1,l.media_id,u.id,r.source_key,r.vod_id,$5,$6,$7
 FROM resource r LEFT JOIN resource_media_links l USING(source_key,vod_id)
 LEFT JOIN media_units u ON u.id=$4 AND u.media_id=l.media_id
 WHERE r.source_key=$2 AND r.vod_id=$3 AND r.vod_play_url<>'' AND md5(r.vod_play_url)=$5
 AND r.resource_status NOT IN('removed','retired','deleted')
 AND ($4=0 OR u.id IS NOT NULL)
 AND EXISTS(SELECT 1 FROM sites s WHERE s.key=r.source_key AND s.enabled)
 ON CONFLICT(attempt_id) DO NOTHING RETURNING source_key,vod_id,playback_version,succeeded,load_ms,created_at
 ) UPDATE vod_items r SET
 total_load_ms=r.total_load_ms+i.load_ms,
 success_count=r.success_count+CASE WHEN i.succeeded THEN 1 ELSE 0 END,
 failure_count=r.failure_count+CASE WHEN i.succeeded THEN 0 ELSE 1 END,
 last_played_at=i.created_at,
 last_success_at=CASE WHEN i.succeeded THEN i.created_at ELSE r.last_success_at END
 FROM inserted i WHERE r.source_key=i.source_key AND r.vod_id=i.vod_id AND md5(r.vod_play_url)=i.playback_version`,
		event.AttemptID, event.SourceKey, event.VodID, event.MediaUnitID, event.PlaybackVersion, success, event.ElapsedMs)
	if err != nil {
		return false, fmt.Errorf("record playback result: %w", err)
	}
	return count > 0, nil
}
