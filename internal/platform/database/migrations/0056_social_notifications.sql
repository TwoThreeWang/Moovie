CREATE TABLE social_notifications (
    id bigserial PRIMARY KEY,
    recipient_user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type text NOT NULL CHECK (type IN ('comment_like', 'comment_reply')),
    user_movie_id bigint NOT NULL REFERENCES user_movies(id) ON DELETE CASCADE,
    reply_id bigint REFERENCES comment_replies(id) ON DELETE CASCADE,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    CHECK ((type = 'comment_like' AND reply_id IS NULL) OR (type = 'comment_reply' AND reply_id IS NOT NULL))
);

CREATE UNIQUE INDEX social_notifications_event_unique
    ON social_notifications (type, actor_user_id, user_movie_id, COALESCE(reply_id, 0));

CREATE INDEX social_notifications_recipient_created
    ON social_notifications (recipient_user_id, created_at DESC);

CREATE INDEX social_notifications_recipient_unread
    ON social_notifications (recipient_user_id, created_at DESC)
    WHERE read_at IS NULL;
