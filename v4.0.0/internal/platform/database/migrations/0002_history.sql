CREATE TABLE watch_histories (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    douban_id text NOT NULL DEFAULT '',
    vod_id text NOT NULL DEFAULT '',
    title text NOT NULL DEFAULT '',
    poster text NOT NULL DEFAULT '',
    episode text NOT NULL DEFAULT '',
    progress bigint NOT NULL DEFAULT 0,
    last_time double precision NOT NULL DEFAULT 0,
    duration double precision NOT NULL DEFAULT 0,
    source text NOT NULL DEFAULT '',
    watched_at timestamptz NOT NULL,
    UNIQUE (user_id, source, vod_id)
);

CREATE INDEX idx_watch_histories_user_watched ON watch_histories (user_id, watched_at DESC);
CREATE INDEX idx_watch_histories_douban_id ON watch_histories (douban_id);
