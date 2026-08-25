-- 开启任何 V2 读取路径前，先完成附加式身份基础结构。
-- 这些表不会替换旧 movies、watch_histories 或 resource_episodes，
-- 而是提供可以先审计的稳定桥接层。

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS legacy_media_mappings (
    legacy_movie_id BIGINT PRIMARY KEY,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    douban_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_id)
);

CREATE TABLE IF NOT EXISTS media_aliases (
    id BIGSERIAL PRIMARY KEY,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    alias TEXT NOT NULL,
    normalized_alias TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'legacy',
    alias_type TEXT NOT NULL DEFAULT 'title',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_id, normalized_alias)
);
CREATE INDEX IF NOT EXISTS media_aliases_media_idx ON media_aliases (media_id);
CREATE INDEX IF NOT EXISTS media_aliases_normalized_trgm_idx
    ON media_aliases USING GIN (normalized_alias gin_trgm_ops);

CREATE TABLE IF NOT EXISTS media_units (
    id BIGSERIAL PRIMARY KEY,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    unit_type TEXT NOT NULL CHECK (unit_type IN ('feature', 'episode', 'special', 'trailer')),
    season_number INTEGER NOT NULL DEFAULT 0,
    episode_number INTEGER,
    absolute_number INTEGER,
    episode_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    air_date DATE,
    runtime_minutes INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_id, unit_type, season_number, episode_key)
);
CREATE INDEX IF NOT EXISTS media_units_media_order_idx
    ON media_units (media_id, season_number, episode_number, id);

ALTER TABLE resource_episodes
    ADD COLUMN IF NOT EXISTS media_unit_id BIGINT REFERENCES media_units(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS resource_episodes_media_unit_idx
    ON resource_episodes (media_unit_id)
    WHERE media_unit_id IS NOT NULL;

ALTER TABLE watch_histories
    ADD COLUMN IF NOT EXISTS media_unit_id BIGINT REFERENCES media_units(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS watch_histories_media_unit_idx
    ON watch_histories (user_id, media_unit_id)
    WHERE media_unit_id IS NOT NULL;

INSERT INTO legacy_media_mappings (legacy_movie_id, media_id, douban_id)
SELECT legacy.id, canonical.id, legacy.douban_id
FROM movies legacy
JOIN media canonical ON canonical.douban_id = legacy.douban_id
ON CONFLICT (legacy_movie_id) DO UPDATE SET
    media_id = EXCLUDED.media_id,
    douban_id = EXCLUDED.douban_id;

INSERT INTO media_aliases (media_id, alias, normalized_alias, source, alias_type)
SELECT id, title, LOWER(REGEXP_REPLACE(NORMALIZE(title, NFKC), '[[:space:][:punct:]]+', '', 'g')), 'legacy', 'title'
FROM media
WHERE title <> ''
ON CONFLICT (media_id, normalized_alias) DO UPDATE SET
    alias = EXCLUDED.alias,
    updated_at = NOW();

INSERT INTO media_aliases (media_id, alias, normalized_alias, source, alias_type)
SELECT id, original_title, LOWER(REGEXP_REPLACE(NORMALIZE(original_title, NFKC), '[[:space:][:punct:]]+', '', 'g')), 'legacy', 'original_title'
FROM media
WHERE original_title <> ''
ON CONFLICT (media_id, normalized_alias) DO UPDATE SET
    alias = EXCLUDED.alias,
    updated_at = NOW();

-- 既有结构化剧集行比重新解析原始 vod_play_url 更可靠。
-- 将它们回填为剧集单元，但不改变当前播放路由使用的旧剧集身份。
INSERT INTO media_units (media_id, unit_type, season_number, episode_key, title)
SELECT DISTINCT ON (media_id, season_number, episode_key)
    media_id, 'episode', season_number, episode_key, episode_label
FROM resource_episodes
WHERE media_id IS NOT NULL AND episode_key <> ''
ORDER BY media_id, season_number, episode_key, updated_at DESC
ON CONFLICT (media_id, unit_type, season_number, episode_key) DO UPDATE SET
    title = CASE WHEN media_units.title = '' THEN EXCLUDED.title ELSE media_units.title END,
    updated_at = NOW();

UPDATE resource_episodes episode
SET media_unit_id = unit.id
FROM media_units unit
WHERE episode.media_unit_id IS NULL
  AND episode.media_id = unit.media_id
  AND episode.season_number = unit.season_number
  AND episode.episode_key = unit.episode_key
  AND unit.unit_type = 'episode';

UPDATE watch_histories history
SET media_unit_id = unit.id
FROM media_units unit
WHERE history.media_unit_id IS NULL
  AND history.media_id = unit.media_id
  AND history.season_number = unit.season_number
  AND history.episode_key = unit.episode_key;
