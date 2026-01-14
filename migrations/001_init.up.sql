-- 用户表
CREATE TABLE users (
    id            SERIAL PRIMARY KEY,
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role          VARCHAR(20) DEFAULT 'user',
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 豆瓣电影信息表
CREATE TABLE movies (
    id             SERIAL PRIMARY KEY,
    douban_id      VARCHAR(20) UNIQUE NOT NULL,
    title          VARCHAR(255) NOT NULL,
    original_title VARCHAR(255),
    year           VARCHAR(10),
    poster         TEXT,
    rating         DECIMAL(3,1),
    genres         TEXT[],
    countries      TEXT[],
    directors      JSONB,
    actors         JSONB,
    summary        TEXT,
    duration       VARCHAR(50),
    imdb_id        VARCHAR(20),
    updated_at     TIMESTAMPTZ DEFAULT NOW()
);

-- 搜索缓存表（含 m3u8 链接）
CREATE TABLE search_cache (
    id            SERIAL PRIMARY KEY,
    keyword       VARCHAR(255) NOT NULL,
    source        VARCHAR(50) NOT NULL,
    result_json   JSONB NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    expires_at    TIMESTAMPTZ NOT NULL,
    UNIQUE(keyword, source)
);

-- 收藏表
CREATE TABLE favorites (
    id            SERIAL PRIMARY KEY,
    user_id       INTEGER REFERENCES users(id) ON DELETE CASCADE,
    movie_id      INTEGER REFERENCES movies(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, movie_id)
);

-- 观影历史表
CREATE TABLE watch_history (
    id            SERIAL PRIMARY KEY,
    user_id       INTEGER REFERENCES users(id) ON DELETE CASCADE,
    douban_id     VARCHAR(20) NOT NULL,
    title         VARCHAR(255),
    poster        TEXT,
    episode       VARCHAR(50),
    progress      INTEGER DEFAULT 0,
    source        VARCHAR(50),
    watched_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, douban_id, episode)
);

-- 搜索日志表（热搜榜）
CREATE TABLE search_logs (
    id            SERIAL PRIMARY KEY,
    keyword       VARCHAR(255) NOT NULL,
    user_id       INTEGER,
    ip_hash       VARCHAR(64),
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 反馈表
CREATE TABLE feedbacks (
    id            SERIAL PRIMARY KEY,
    user_id       INTEGER,
    type          VARCHAR(20),
    content       TEXT NOT NULL,
    movie_url     TEXT,
    status        VARCHAR(20) DEFAULT 'pending',
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_search_cache_keyword ON search_cache(keyword, source);
CREATE INDEX idx_search_cache_expires ON search_cache(expires_at);
CREATE INDEX idx_search_logs_keyword ON search_logs(keyword);
CREATE INDEX idx_search_logs_created ON search_logs(created_at);
CREATE INDEX idx_movies_douban_id ON movies(douban_id);
CREATE INDEX idx_watch_history_user ON watch_history(user_id, watched_at DESC);
