-- 为同一剧集保留多条 Provider 线路。
-- 旧 resource_episodes 表继续作为默认线路的兼容投影。

CREATE TABLE IF NOT EXISTS resource_play_lines (
    id BIGSERIAL PRIMARY KEY,
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    line_key TEXT NOT NULL,
    line_label TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    resource_status TEXT NOT NULL DEFAULT 'active',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_key, vod_id, line_key),
    FOREIGN KEY (source_key, vod_id) REFERENCES vod_items(source_key, vod_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS resource_episode_candidates (
    id BIGSERIAL PRIMARY KEY,
    line_id BIGINT NOT NULL REFERENCES resource_play_lines(id) ON DELETE CASCADE,
    media_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    media_unit_id BIGINT REFERENCES media_units(id) ON DELETE SET NULL,
    season_number INTEGER NOT NULL DEFAULT 1,
    episode_key TEXT NOT NULL,
    episode_label TEXT NOT NULL DEFAULT '',
    play_url TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL DEFAULT '',
    quality TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    resource_status TEXT NOT NULL DEFAULT 'active',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (line_id, season_number, episode_key)
);
CREATE INDEX IF NOT EXISTS resource_episode_candidates_media_idx
    ON resource_episode_candidates (media_id, season_number, episode_key)
    WHERE media_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS resource_episode_candidates_unit_idx
    ON resource_episode_candidates (media_unit_id)
    WHERE media_unit_id IS NOT NULL;

INSERT INTO resource_play_lines (source_key, vod_id, line_key, line_label, sort_order, last_seen_at)
SELECT source_key, vod_id, 'default', '默认源', 0, COALESCE(MAX(last_seen_at), NOW())
FROM resource_episodes
GROUP BY source_key, vod_id
ON CONFLICT (source_key, vod_id, line_key) DO UPDATE SET
    last_seen_at = GREATEST(resource_play_lines.last_seen_at, EXCLUDED.last_seen_at),
    updated_at = NOW();

INSERT INTO resource_episode_candidates
(line_id, media_id, media_unit_id, season_number, episode_key, episode_label,
 play_url, format, quality, sort_order, resource_status, last_seen_at, last_accessed_at)
SELECT line.id, episode.media_id, episode.media_unit_id, episode.season_number,
       episode.episode_key, episode.episode_label, episode.play_url, episode.format,
       episode.quality, episode.sort_order, episode.resource_status,
       COALESCE(episode.last_seen_at, NOW()), episode.last_accessed_at
FROM resource_episodes episode
JOIN resource_play_lines line ON line.source_key = episode.source_key
 AND line.vod_id = episode.vod_id AND line.line_key = 'default'
ON CONFLICT (line_id, season_number, episode_key) DO UPDATE SET
    media_id = COALESCE(EXCLUDED.media_id, resource_episode_candidates.media_id),
    media_unit_id = COALESCE(EXCLUDED.media_unit_id, resource_episode_candidates.media_unit_id),
    episode_label = EXCLUDED.episode_label,
    play_url = EXCLUDED.play_url,
    format = EXCLUDED.format,
    quality = EXCLUDED.quality,
    resource_status = EXCLUDED.resource_status,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = NOW();
