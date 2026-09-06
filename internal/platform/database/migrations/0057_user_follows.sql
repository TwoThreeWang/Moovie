-- 关注关系：社区的复访引擎。片友推荐算出了口味相近的人，
-- 但没有这张表就无法把关系沉淀下来，也就没有 feed。
CREATE TABLE user_follows (
    follower_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);

-- 查「谁关注了我」和粉丝数。
CREATE INDEX idx_user_follows_followee ON user_follows (followee_id, created_at DESC);
