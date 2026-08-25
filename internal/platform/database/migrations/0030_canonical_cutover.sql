-- 0030_canonical_cutover.sql：为一次性数据迁移准备最终列与索引。
-- 这里不再自行复制旧表数据；所有业务转换只由 cmd/datamigrate 完成，
-- 避免 SQL 与 Go 各自实现一套规则后产生重复或不同的剧集身份。

ALTER TABLE media ADD COLUMN IF NOT EXISTS reviews_json TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN IF NOT EXISTS reviews_updated_at TIMESTAMPTZ NOT NULL DEFAULT TO_TIMESTAMP(0);

ALTER TABLE user_movies ADD COLUMN IF NOT EXISTS media_id BIGINT REFERENCES media(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS user_movies_media_idx ON user_movies (user_id, media_id, status);

CREATE INDEX IF NOT EXISTS media_rating_idx ON media (rating_douban DESC);
CREATE INDEX IF NOT EXISTS media_updated_idx ON media (updated_at DESC);
CREATE INDEX IF NOT EXISTS media_embedding_hnsw ON media USING hnsw (embedding vector_l2_ops);
