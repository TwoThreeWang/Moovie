-- 「为你推荐」结果按用户持久化，避免 Web 重启后同步重算多次向量查询。
-- payload 保存页面渲染所需的精简结果；完整生成后一次 UPSERT，不暴露半份快照。
CREATE TABLE user_recommendation_snapshots (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    payload JSONB NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
