-- 本站热播复用现有热门快照；Web 不再实时聚合最近 7 天播放事件。
ALTER TABLE popularity_snapshot_runs
    DROP CONSTRAINT popularity_snapshot_runs_media_type_check;
ALTER TABLE popularity_snapshot_runs
    ADD CONSTRAINT popularity_snapshot_runs_media_type_check
    CHECK (media_type IN ('movie', 'tv', 'show', 'cartoon', 'trending'));

-- Worker 每次只扫描 played_10s 的最近 7 天数据。
CREATE INDEX playback_attempt_events_trending_idx
    ON playback_attempt_events (created_at DESC, media_id)
    WHERE event_type = 'played_10s';
