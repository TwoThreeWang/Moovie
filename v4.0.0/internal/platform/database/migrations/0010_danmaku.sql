CREATE TABLE danmakus (
    id bigserial PRIMARY KEY,
    vod_key varchar(255) NOT NULL,
    time double precision NOT NULL CHECK (time >= 0),
    text varchar(100) NOT NULL CHECK (char_length(text) BETWEEN 1 AND 50),
    mode smallint NOT NULL CHECK (mode IN (0, 1, 2)),
    color varchar(7) NOT NULL CHECK (color ~ '^#[0-9A-F]{6}$'),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    deleted boolean NOT NULL DEFAULT FALSE,
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_danmakus_lookup ON danmakus (vod_key, deleted, time ASC);
CREATE INDEX idx_danmakus_user_created ON danmakus (user_id, created_at DESC);
CREATE INDEX idx_danmakus_recent ON danmakus (created_at DESC) WHERE deleted = FALSE;
