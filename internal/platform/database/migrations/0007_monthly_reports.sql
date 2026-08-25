CREATE TABLE monthly_reports (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    year_month text NOT NULL,
    watched_count integer NOT NULL DEFAULT 0,
    total_duration_minutes integer NOT NULL DEFAULT 0,
    avg_rating double precision NOT NULL DEFAULT 0,
    genre_stats text NOT NULL DEFAULT '[]',
    top_movie_id text NOT NULL DEFAULT '',
    top_movie_title text NOT NULL DEFAULT '',
    top_movie_poster text NOT NULL DEFAULT '',
    top_movie_rating integer NOT NULL DEFAULT 0,
    continuous_days integer NOT NULL DEFAULT 0,
    persona_title text NOT NULL DEFAULT '',
    persona_line text NOT NULL DEFAULT '',
    percentile_rank integer NOT NULL DEFAULT 0,
    featured_quote text NOT NULL DEFAULT '',
    poster_wall text NOT NULL DEFAULT '[]',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'generating', 'generated', 'failed')),
    error_message text NOT NULL DEFAULT '',
    generated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, year_month)
);

CREATE INDEX idx_monthly_reports_user_generated ON monthly_reports (user_id, year_month DESC)
WHERE status = 'generated';
CREATE INDEX idx_monthly_reports_pending ON monthly_reports (status, created_at);
