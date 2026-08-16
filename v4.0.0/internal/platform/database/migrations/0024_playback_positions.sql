-- 规范播放进度是附加结构。在 HISTORY_V2_READ 开启前，
-- watch_histories 仍是兼容读写模型。

CREATE SEQUENCE IF NOT EXISTS playback_position_version_seq;

ALTER TABLE history_sync_events
    ADD COLUMN IF NOT EXISTS media_unit_id BIGINT REFERENCES media_units(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS history_sync_events_media_unit_idx
    ON history_sync_events (user_id, media_unit_id, version)
    WHERE media_unit_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS playback_positions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    media_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    media_unit_id BIGINT REFERENCES media_units(id) ON DELETE SET NULL,
    legacy_history_id BIGINT,
    position_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    progress_percent INTEGER NOT NULL DEFAULT 0 CHECK (progress_percent BETWEEN 0 AND 100),
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    last_source_key TEXT NOT NULL DEFAULT '',
    last_vod_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    poster TEXT NOT NULL DEFAULT '',
    episode TEXT NOT NULL DEFAULT '',
    season_number INTEGER NOT NULL DEFAULT 1,
    episode_key TEXT NOT NULL DEFAULT '',
    activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    server_version BIGINT NOT NULL DEFAULT nextval('playback_position_version_seq'),
    deleted_at TIMESTAMPTZ,
    legacy_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS playback_positions_unit_uidx
    ON playback_positions (user_id, media_unit_id)
    WHERE media_unit_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS playback_positions_media_episode_uidx
    ON playback_positions (user_id, media_id, season_number, episode_key)
    WHERE media_unit_id IS NULL AND media_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS playback_positions_legacy_uidx
    ON playback_positions (user_id, last_source_key, last_vod_id, season_number, episode_key)
    WHERE media_unit_id IS NULL AND media_id IS NULL;
CREATE INDEX IF NOT EXISTS playback_positions_continue_idx
    ON playback_positions (user_id, activity_at DESC)
    WHERE deleted_at IS NULL AND completed = FALSE;
CREATE INDEX IF NOT EXISTS playback_positions_version_idx
    ON playback_positions (user_id, server_version);
CREATE INDEX IF NOT EXISTS playback_positions_tombstone_idx
    ON playback_positions (deleted_at)
    WHERE deleted_at IS NOT NULL;

INSERT INTO playback_positions (
    user_id, media_id, media_unit_id, legacy_history_id, position_seconds, duration_seconds, progress_percent,
    completed, last_source_key, last_vod_id, title, poster, episode, season_number,
    episode_key, activity_at, legacy_payload
)
SELECT DISTINCT ON (history.user_id, history.media_unit_id)
    history.user_id, history.media_id, history.media_unit_id, history.id, history.last_time, history.duration,
    LEAST(100, GREATEST(0, history.progress)), history.progress >= 100,
    history.source, history.vod_id, history.title, history.poster, history.episode,
    GREATEST(history.season_number, 1), history.episode_key,
    COALESCE(history.updated_at, history.watched_at, NOW()), to_jsonb(history)
FROM watch_histories history
WHERE history.media_unit_id IS NOT NULL
ORDER BY history.user_id, history.media_unit_id, COALESCE(history.updated_at, history.watched_at) DESC
ON CONFLICT (user_id, media_unit_id) WHERE media_unit_id IS NOT NULL DO NOTHING;

INSERT INTO playback_positions (
    user_id, media_id, legacy_history_id, position_seconds, duration_seconds, progress_percent, completed,
    last_source_key, last_vod_id, title, poster, episode, season_number, episode_key,
    activity_at, legacy_payload
)
SELECT DISTINCT ON (history.user_id, history.media_id, history.season_number, history.episode_key)
    history.user_id, history.media_id, history.id, history.last_time, history.duration,
    LEAST(100, GREATEST(0, history.progress)), history.progress >= 100,
    history.source, history.vod_id, history.title, history.poster, history.episode,
    GREATEST(history.season_number, 1), history.episode_key,
    COALESCE(history.updated_at, history.watched_at, NOW()), to_jsonb(history)
FROM watch_histories history
WHERE history.media_unit_id IS NULL AND history.media_id IS NOT NULL
ORDER BY history.user_id, history.media_id, history.season_number,
         history.episode_key, COALESCE(history.updated_at, history.watched_at) DESC
ON CONFLICT (user_id, media_id, season_number, episode_key)
    WHERE media_unit_id IS NULL AND media_id IS NOT NULL DO NOTHING;

INSERT INTO playback_positions (
    user_id, legacy_history_id, position_seconds, duration_seconds, progress_percent, completed,
    last_source_key, last_vod_id, title, poster, episode, season_number, episode_key,
    activity_at, legacy_payload
)
SELECT DISTINCT ON (history.user_id, history.source, history.vod_id, history.season_number, history.episode_key)
    history.user_id, history.id, history.last_time, history.duration,
    LEAST(100, GREATEST(0, history.progress)), history.progress >= 100,
    history.source, history.vod_id, history.title, history.poster, history.episode,
    GREATEST(history.season_number, 1), history.episode_key,
    COALESCE(history.updated_at, history.watched_at, NOW()), to_jsonb(history)
FROM watch_histories history
WHERE history.media_unit_id IS NULL AND history.media_id IS NULL
  AND history.source <> '' AND history.vod_id <> ''
ORDER BY history.user_id, history.source, history.vod_id, history.season_number,
         history.episode_key, COALESCE(history.updated_at, history.watched_at) DESC
ON CONFLICT (user_id, last_source_key, last_vod_id, season_number, episode_key)
    WHERE media_unit_id IS NULL AND media_id IS NULL DO NOTHING;
