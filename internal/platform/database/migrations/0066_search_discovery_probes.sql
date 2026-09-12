-- 记录"这个关键词已经问过豆瓣联想了"，用于跨实例、跨重启地去重。
-- 进程内搜索缓存只有几百条且重启即失效，量大时挡不住重复的长尾关键词，
-- 每个未命中的词都会变成一次豆瓣请求。
CREATE TABLE search_discovery_probes (
    keyword text PRIMARY KEY,
    probed_at timestamptz NOT NULL DEFAULT NOW()
);

-- 清理任务按 probed_at 批量删除过期行。
CREATE INDEX idx_search_discovery_probes_probed_at ON search_discovery_probes (probed_at);
