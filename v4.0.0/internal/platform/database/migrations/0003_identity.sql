CREATE TABLE users (
    id bigserial PRIMARY KEY,
    email text NOT NULL UNIQUE,
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'user',
    douban_user_id text NOT NULL DEFAULT '',
    is_public boolean NOT NULL DEFAULT false,
    avatar text NOT NULL DEFAULT '🎬',
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_douban_user_id ON users (douban_user_id);
