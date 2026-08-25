CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE movies (
    id bigserial PRIMARY KEY,
    douban_id text NOT NULL UNIQUE,
    title text NOT NULL DEFAULT '',
    original_title text NOT NULL DEFAULT '',
    year text NOT NULL DEFAULT '',
    poster text NOT NULL DEFAULT '',
    rating double precision NOT NULL DEFAULT 0,
    genres text NOT NULL DEFAULT '',
    countries text NOT NULL DEFAULT '',
    directors text NOT NULL DEFAULT '',
    actors text NOT NULL DEFAULT '',
    summary text NOT NULL DEFAULT '',
    duration text NOT NULL DEFAULT '',
    imdb_id text NOT NULL DEFAULT '',
    backdrops text NOT NULL DEFAULT '',
    embedding_content text NOT NULL DEFAULT '',
    embedding vector(768),
    reviews_json text NOT NULL DEFAULT '',
    reviews_updated_at timestamptz NOT NULL DEFAULT to_timestamp(0),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_movies_rating ON movies (rating DESC);
CREATE INDEX idx_movies_updated_at ON movies (updated_at DESC);
CREATE INDEX idx_movies_embedding_hnsw ON movies USING hnsw (embedding vector_l2_ops);
