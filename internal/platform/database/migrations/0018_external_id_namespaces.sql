-- Provider ID 只在媒体类型命名空间内唯一。TMDB movie 101 和 TMDB tv 101
-- 可能表示不同作品；继续使用旧的仅 Provider 约束，会在刷新时把 ID 静默移动到另一媒体行。

ALTER TABLE media_external_ids
    ADD COLUMN IF NOT EXISTS external_type TEXT NOT NULL DEFAULT '';

UPDATE media_external_ids external
SET external_type = CASE
    WHEN media.media_type IN ('tv', 'series', 'season', 'show', 'animation') THEN 'tv'
    ELSE 'movie'
END
FROM media
WHERE media.id = external.media_id
  AND external.external_type = '';

ALTER TABLE media_external_ids
    DROP CONSTRAINT IF EXISTS media_external_ids_provider_external_id_key;
ALTER TABLE media_external_ids
    DROP CONSTRAINT IF EXISTS media_external_ids_media_id_provider_key;

CREATE UNIQUE INDEX IF NOT EXISTS media_external_ids_provider_type_external_uidx
    ON media_external_ids (provider, external_type, external_id);
CREATE UNIQUE INDEX IF NOT EXISTS media_external_ids_media_provider_type_uidx
    ON media_external_ids (media_id, provider, external_type);
