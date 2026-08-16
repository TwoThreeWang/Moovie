CREATE TABLE feedbacks (
    id bigserial PRIMARY KEY,
    user_id bigint REFERENCES users(id) ON DELETE SET NULL,
    type varchar(32) NOT NULL CHECK (type IN ('bug', 'request', 'suggestion', 'dmca')),
    content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 5000),
    movie_url text NOT NULL DEFAULT '',
    status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolved', 'rejected')),
    reply text NOT NULL DEFAULT '',
    replied_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_feedbacks_created_at ON feedbacks (created_at DESC);
CREATE INDEX idx_feedbacks_status_created ON feedbacks (status, created_at DESC);
CREATE INDEX idx_feedbacks_user_created ON feedbacks (user_id, created_at DESC);
