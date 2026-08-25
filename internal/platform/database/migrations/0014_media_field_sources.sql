-- 记录字段级来源，使豆瓣/TMDB 元数据合并结果确定且可解释。
-- 规范值仍保存在 media；本表记录每个字段的获胜 Provider，
-- 防止后续低优先级刷新覆盖它。

CREATE TABLE IF NOT EXISTS media_field_sources (
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    field_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    value_hash TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_id, field_name)
);
CREATE INDEX IF NOT EXISTS media_field_sources_provider_idx
    ON media_field_sources (provider, observed_at DESC);
