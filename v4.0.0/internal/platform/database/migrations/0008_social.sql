CREATE TABLE comment_likes (
    id bigserial PRIMARY KEY,
    user_movie_id bigint NOT NULL REFERENCES user_movies(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (user_movie_id, user_id)
);

CREATE INDEX idx_comment_likes_user_movie_id ON comment_likes (user_movie_id);

CREATE TABLE comment_replies (
    id bigserial PRIMARY KEY,
    user_movie_id bigint NOT NULL REFERENCES user_movies(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 300),
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_comment_replies_user_movie_created ON comment_replies (user_movie_id, created_at ASC);
