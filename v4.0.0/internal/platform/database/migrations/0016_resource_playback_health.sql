-- 保存剧集级用户播放健康样本。旧 vod_items 计数和上报端点保持不变，
-- 本表只增加更细粒度信号。

CREATE TABLE IF NOT EXISTS resource_playback_health (
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    media_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    season_number INTEGER NOT NULL DEFAULT 1,
    episode_key TEXT NOT NULL,
    success_count BIGINT NOT NULL DEFAULT 0,
    failure_count BIGINT NOT NULL DEFAULT 0,
    total_count BIGINT NOT NULL DEFAULT 0,
    avg_load_ms BIGINT NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_key, vod_id, season_number, episode_key),
    FOREIGN KEY (source_key, vod_id) REFERENCES vod_items(source_key, vod_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS resource_playback_health_media_idx
    ON resource_playback_health (media_id, season_number, episode_key, updated_at DESC);
