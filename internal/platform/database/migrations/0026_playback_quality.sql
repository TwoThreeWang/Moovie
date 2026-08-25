-- 保存幂等播放尝试事件和按小时汇总的候选质量。
-- 分阶段灰度期间旧加载速度计数器继续可写。

CREATE TABLE IF NOT EXISTS playback_attempt_events (
    id BIGSERIAL PRIMARY KEY,
    attempt_id TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'attempt_started', 'manifest_loaded', 'first_frame', 'played_10s',
        'rebuffer', 'fatal_error', 'source_switched', 'ended', 'abandoned'
    )),
    candidate_id BIGINT NOT NULL REFERENCES resource_episode_candidates(id) ON DELETE CASCADE,
    play_line_id BIGINT NOT NULL REFERENCES resource_play_lines(id) ON DELETE CASCADE,
    media_unit_id BIGINT NOT NULL REFERENCES media_units(id) ON DELETE CASCADE,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    elapsed_ms INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (attempt_id, event_type)
);
CREATE INDEX IF NOT EXISTS playback_attempt_events_candidate_idx
    ON playback_attempt_events (candidate_id, created_at DESC);
CREATE INDEX IF NOT EXISTS playback_attempt_events_unit_idx
    ON playback_attempt_events (media_unit_id, created_at DESC);
CREATE INDEX IF NOT EXISTS playback_attempt_events_created_idx
    ON playback_attempt_events (created_at);

CREATE TABLE IF NOT EXISTS playback_quality_rollups (
    bucket TIMESTAMPTZ NOT NULL,
    candidate_id BIGINT NOT NULL REFERENCES resource_episode_candidates(id) ON DELETE CASCADE,
    play_line_id BIGINT NOT NULL REFERENCES resource_play_lines(id) ON DELETE CASCADE,
    media_unit_id BIGINT NOT NULL REFERENCES media_units(id) ON DELETE CASCADE,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    attempt_count BIGINT NOT NULL DEFAULT 0,
    manifest_count BIGINT NOT NULL DEFAULT 0,
    first_frame_count BIGINT NOT NULL DEFAULT 0,
    first_frame_total_ms BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    failure_count BIGINT NOT NULL DEFAULT 0,
    rebuffer_count BIGINT NOT NULL DEFAULT 0,
    switched_count BIGINT NOT NULL DEFAULT 0,
    ended_count BIGINT NOT NULL DEFAULT 0,
    abandoned_count BIGINT NOT NULL DEFAULT 0,
    timeout_count BIGINT NOT NULL DEFAULT 0,
    network_count BIGINT NOT NULL DEFAULT 0,
    decode_count BIGINT NOT NULL DEFAULT 0,
    http_count BIGINT NOT NULL DEFAULT 0,
    unknown_failure_count BIGINT NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket, candidate_id)
);
CREATE INDEX IF NOT EXISTS playback_quality_rollups_unit_idx
    ON playback_quality_rollups (media_unit_id, bucket DESC);
