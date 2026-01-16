CREATE TABLE search_cache (
    id            SERIAL PRIMARY KEY,
    keyword       VARCHAR(255) NOT NULL,
    source        VARCHAR(50) NOT NULL,
    result_json   JSONB NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    expires_at    TIMESTAMPTZ NOT NULL,
    UNIQUE(keyword, source)
);

CREATE INDEX idx_search_cache_keyword ON search_cache(keyword, source);
CREATE INDEX idx_search_cache_expires ON search_cache(expires_at);
