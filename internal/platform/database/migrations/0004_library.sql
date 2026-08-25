CREATE TABLE user_movies (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    movie_id text NOT NULL,
    title text NOT NULL DEFAULT '',
    poster text NOT NULL DEFAULT '',
    year text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('wish', 'watched')),
    rating integer NOT NULL DEFAULT 0,
    comment text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, movie_id)
);

CREATE INDEX idx_user_movies_user_status_updated ON user_movies (user_id, status, updated_at DESC);
