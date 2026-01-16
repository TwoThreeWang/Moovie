-- 资源网视频数据表
CREATE TABLE vod_items (
    id              SERIAL PRIMARY KEY,
    source_key      VARCHAR(50) NOT NULL,
    vod_id          VARCHAR(50) NOT NULL,
    vod_name        VARCHAR(255) NOT NULL,
    vod_sub         VARCHAR(255),
    vod_en          VARCHAR(255),
    vod_tag         VARCHAR(255),
    vod_class       VARCHAR(100),
    vod_pic         TEXT,
    vod_actor       TEXT,
    vod_director    VARCHAR(255),
    vod_blurb       TEXT,
    vod_remarks     VARCHAR(255),
    vod_pubdate     VARCHAR(50),
    vod_total       VARCHAR(50),
    vod_serial      VARCHAR(50),
    vod_area        VARCHAR(100),
    vod_lang        VARCHAR(100),
    vod_year        VARCHAR(20),
    vod_duration    VARCHAR(50),
    vod_time        VARCHAR(50),
    vod_douban_id   VARCHAR(50),
    vod_content     TEXT,
    vod_play_url    TEXT,
    type_name       VARCHAR(100),
    last_visited_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(source_key, vod_id)
);

CREATE INDEX idx_vod_items_last_visited ON vod_items(last_visited_at);
CREATE INDEX idx_vod_items_source_vod ON vod_items(source_key, vod_id);
CREATE INDEX idx_vod_items_name ON vod_items(vod_name);
