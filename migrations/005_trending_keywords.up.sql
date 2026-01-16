-- 热搜关键词统计表
CREATE TABLE trending_keywords (
    keyword          VARCHAR(255) PRIMARY KEY,
    count            INTEGER DEFAULT 1,
    last_searched_at TIMESTAMPTZ DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_trending_keywords_count ON trending_keywords(count DESC);
CREATE INDEX idx_trending_keywords_last_searched ON trending_keywords(last_searched_at);
