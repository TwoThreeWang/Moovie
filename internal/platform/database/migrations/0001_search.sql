CREATE TABLE sites (
    id bigserial PRIMARY KEY,
    key text NOT NULL UNIQUE,
    base_url text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at bigint NOT NULL DEFAULT 0,
    updated_at bigint NOT NULL DEFAULT 0
);

CREATE TABLE vod_items (
    source_key text NOT NULL,
    vod_id text NOT NULL,
    vod_name text NOT NULL DEFAULT '',
    vod_sub text NOT NULL DEFAULT '',
    vod_en text NOT NULL DEFAULT '',
    vod_tag text NOT NULL DEFAULT '',
    vod_class text NOT NULL DEFAULT '',
    vod_pic text NOT NULL DEFAULT '',
    vod_actor text NOT NULL DEFAULT '',
    vod_director text NOT NULL DEFAULT '',
    vod_blurb text NOT NULL DEFAULT '',
    vod_remarks text NOT NULL DEFAULT '',
    vod_pubdate text NOT NULL DEFAULT '',
    vod_total text NOT NULL DEFAULT '',
    vod_serial text NOT NULL DEFAULT '',
    vod_area text NOT NULL DEFAULT '',
    vod_lang text NOT NULL DEFAULT '',
    vod_year text NOT NULL DEFAULT '',
    vod_duration text NOT NULL DEFAULT '',
    vod_time text NOT NULL DEFAULT '',
    vod_douban_id text NOT NULL DEFAULT '',
    vod_content text NOT NULL DEFAULT '',
    vod_play_url text NOT NULL DEFAULT '',
    type_name text NOT NULL DEFAULT '',
    last_visited_at timestamptz NOT NULL DEFAULT NOW(),
    avg_speed_ms bigint NOT NULL DEFAULT 0,
    sample_count bigint NOT NULL DEFAULT 0,
    failed_count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (source_key, vod_id)
);

CREATE INDEX idx_vod_items_last_visited_at ON vod_items (last_visited_at DESC);

CREATE TABLE copyright_filters (
    id bigserial PRIMARY KEY,
    keyword text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE category_filters (
    id bigserial PRIMARY KEY,
    keyword text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE search_logs (
    id bigserial PRIMARY KEY,
    keyword text NOT NULL,
    user_id bigint,
    ip_hash text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_search_logs_created_at ON search_logs (created_at DESC);

CREATE TABLE trending_keywords (
    keyword text PRIMARY KEY,
    count bigint NOT NULL DEFAULT 0,
    last_searched_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE site_stats (
    id bigserial PRIMARY KEY,
    site_key text NOT NULL,
    bucket timestamptz NOT NULL,
    ok_count bigint NOT NULL DEFAULT 0,
    empty_count bigint NOT NULL DEFAULT 0,
    timeout_count bigint NOT NULL DEFAULT 0,
    error_count bigint NOT NULL DEFAULT 0,
    total_ms bigint NOT NULL DEFAULT 0,
    UNIQUE (site_key, bucket)
);

CREATE INDEX idx_site_stats_bucket ON site_stats (bucket);
