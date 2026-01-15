-- 站点配置表
CREATE TABLE sites (
    id         SERIAL PRIMARY KEY,
    key        VARCHAR(50) UNIQUE NOT NULL,
    base_url   VARCHAR(500) NOT NULL,
    enabled    BOOLEAN DEFAULT true,
    created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW()),
    updated_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())
);

CREATE INDEX idx_sites_enabled ON sites(enabled);
