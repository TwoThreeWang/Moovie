-- 0013_media_identity.sql：建立规范媒体身份。
-- 旧 movies、vod_items 和 watch_histories 继续可读，并在下方回填到新身份层。

CREATE TABLE IF NOT EXISTS media (
    id BIGSERIAL PRIMARY KEY,
    media_type TEXT NOT NULL DEFAULT 'movie',
    title TEXT NOT NULL DEFAULT '',
    original_title TEXT NOT NULL DEFAULT '',
    year TEXT NOT NULL DEFAULT '',
    poster TEXT NOT NULL DEFAULT '',
    backdrops TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    genres TEXT NOT NULL DEFAULT '',
    countries TEXT NOT NULL DEFAULT '',
    directors TEXT NOT NULL DEFAULT '',
    actors TEXT NOT NULL DEFAULT '',
    duration TEXT NOT NULL DEFAULT '',
    embedding_content TEXT NOT NULL DEFAULT '',
    embedding vector(768),
    rating_douban DOUBLE PRECISION NOT NULL DEFAULT 0,
    rating_tmdb DOUBLE PRECISION NOT NULL DEFAULT 0,
    vote_count_tmdb INTEGER NOT NULL DEFAULT 0,
    metadata_version INTEGER NOT NULL DEFAULT 1,
    metadata_status TEXT NOT NULL DEFAULT 'partial',
    last_metadata_sync_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 豆瓣 ID 不是必填项：以 TMDB/IMDb 为首个来源的记录同样是有效规范媒体。
ALTER TABLE media ADD COLUMN IF NOT EXISTS douban_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS media_douban_uidx
    ON media (douban_id)
    WHERE douban_id <> '';
CREATE INDEX IF NOT EXISTS media_title_year_idx ON media (title, year);

CREATE TABLE IF NOT EXISTS media_external_ids (
    id BIGSERIAL PRIMARY KEY,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    external_id TEXT NOT NULL,
    confidence NUMERIC(5,4) NOT NULL DEFAULT 1.0000,
    is_primary BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, external_id),
    UNIQUE (media_id, provider)
);

CREATE TABLE IF NOT EXISTS resource_media_links (
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    confidence NUMERIC(5,4) NOT NULL DEFAULT 1.0000,
    matched_by TEXT NOT NULL DEFAULT 'douban_id',
    is_locked BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_key, vod_id)
);
CREATE INDEX IF NOT EXISTS resource_media_links_media_idx ON resource_media_links (media_id);

CREATE TABLE IF NOT EXISTS media_source_snapshots (
    id BIGSERIAL PRIMARY KEY,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    payload_hash TEXT NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_success_at TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT '',
    UNIQUE (media_id, provider)
);
CREATE INDEX IF NOT EXISTS media_source_snapshots_provider_idx
    ON media_source_snapshots (provider, fetched_at DESC);

CREATE TABLE IF NOT EXISTS resource_episodes (
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    media_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    season_number INTEGER NOT NULL DEFAULT 1,
    episode_key TEXT NOT NULL,
    episode_label TEXT NOT NULL DEFAULT '',
    play_url TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL DEFAULT '',
    quality TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    last_accessed_at TIMESTAMPTZ,
    resource_status TEXT NOT NULL DEFAULT 'active',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_key, vod_id, season_number, episode_key),
    FOREIGN KEY (source_key, vod_id) REFERENCES vod_items(source_key, vod_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS resource_episodes_lookup_idx
    ON resource_episodes (source_key, vod_id, season_number, sort_order);

ALTER TABLE watch_histories ADD COLUMN IF NOT EXISTS media_id BIGINT REFERENCES media(id) ON DELETE SET NULL;
ALTER TABLE watch_histories ADD COLUMN IF NOT EXISTS season_number INTEGER NOT NULL DEFAULT 1;
ALTER TABLE watch_histories ADD COLUMN IF NOT EXISTS episode_key TEXT NOT NULL DEFAULT '';
ALTER TABLE watch_histories ADD COLUMN IF NOT EXISTS preferred_source_key TEXT NOT NULL DEFAULT '';
ALTER TABLE watch_histories ADD COLUMN IF NOT EXISTS preferred_vod_id TEXT NOT NULL DEFAULT '';
ALTER TABLE watch_histories ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS watch_histories_media_idx ON watch_histories (user_id, media_id, season_number, episode_key);
CREATE INDEX IF NOT EXISTS watch_histories_updated_idx ON watch_histories (user_id, updated_at DESC);

-- 从现有 catalog 回填规范媒体，不删除也不重写旧 movies 表；重复执行仍然安全。
INSERT INTO media (
    media_type, douban_id, title, original_title, year, poster, summary,
    genres, countries, directors, actors, duration, rating_douban,
    metadata_status, created_at, updated_at
)
SELECT
    'movie', m.douban_id, m.title, m.original_title, m.year, m.poster, m.summary,
    m.genres, m.countries, m.directors, m.actors, m.duration, m.rating,
    CASE WHEN m.title <> '' THEN 'partial' ELSE 'empty' END,
    COALESCE(m.updated_at, NOW()), COALESCE(m.updated_at, NOW())
FROM movies m
WHERE m.douban_id <> ''
ON CONFLICT (douban_id) WHERE douban_id <> '' DO UPDATE SET
    title = CASE WHEN media.title = '' THEN EXCLUDED.title ELSE media.title END,
    poster = CASE WHEN media.poster = '' THEN EXCLUDED.poster ELSE media.poster END,
    summary = CASE WHEN media.summary = '' THEN EXCLUDED.summary ELSE media.summary END,
    updated_at = GREATEST(media.updated_at, EXCLUDED.updated_at);

INSERT INTO media_external_ids (media_id, provider, external_id, is_primary)
SELECT m.id, 'imdb', c.imdb_id, TRUE
FROM movies c
JOIN media m ON m.douban_id = c.douban_id
WHERE c.imdb_id <> ''
ON CONFLICT (provider, external_id) DO UPDATE SET
    media_id = EXCLUDED.media_id, is_primary = EXCLUDED.is_primary, updated_at = NOW();

INSERT INTO resource_media_links (source_key, vod_id, media_id, confidence, matched_by, verified_at)
SELECT v.source_key, v.vod_id, m.id, 1.0000, 'douban_id', NOW()
FROM vod_items v
JOIN media m ON m.douban_id = v.vod_douban_id
WHERE v.vod_douban_id <> ''
ON CONFLICT (source_key, vod_id) DO UPDATE SET
    media_id = EXCLUDED.media_id,
    confidence = GREATEST(resource_media_links.confidence, EXCLUDED.confidence),
    matched_by = CASE WHEN resource_media_links.is_locked THEN resource_media_links.matched_by ELSE EXCLUDED.matched_by END,
    updated_at = NOW();

UPDATE watch_histories h
SET media_id = m.id,
    episode_key = CASE WHEN h.episode <> '' THEN h.episode ELSE 'S01E01' END,
    updated_at = COALESCE(h.watched_at, NOW())
FROM media m
WHERE h.media_id IS NULL AND h.douban_id <> '' AND m.douban_id = h.douban_id;
