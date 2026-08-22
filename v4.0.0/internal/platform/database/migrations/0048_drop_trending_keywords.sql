CREATE INDEX IF NOT EXISTS idx_search_logs_keyword ON search_logs (keyword);

DROP TABLE IF EXISTS trending_keywords;
