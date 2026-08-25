-- 每次人工匹配决定都只追加且可追溯。候选与规范关联变更由 Repository 在同一事务中完成。

CREATE TABLE IF NOT EXISTS resource_match_audits (
    id BIGSERIAL PRIMARY KEY,
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    actor_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    decision TEXT NOT NULL CHECK (decision IN ('verified', 'rejected')),
    previous_status TEXT NOT NULL,
    confidence NUMERIC(5,4) NOT NULL,
    match_method TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS resource_match_audits_resource_idx
    ON resource_match_audits (source_key, vod_id, created_at DESC);
CREATE INDEX IF NOT EXISTS resource_match_audits_actor_idx
    ON resource_match_audits (actor_user_id, created_at DESC)
    WHERE actor_user_id IS NOT NULL;
