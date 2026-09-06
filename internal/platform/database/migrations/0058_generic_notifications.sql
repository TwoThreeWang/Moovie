-- 把通知表放宽成通用形状：一条通知就是「谁(actor) 对 谁(recipient) 做了 什么(type)」，
-- 可选地指向一个短评或一条回复。新增通知类型不再需要改表结构。
-- 不加 subject_type/subject_id 这类泛化列：那会丢掉下面几条外键的级联删除，
-- 短评被删后留下指向空处的通知，比多写一个 CHECK 麻烦得多。

-- 关注类通知没有短评主体。
ALTER TABLE social_notifications ALTER COLUMN user_movie_id DROP NOT NULL;

-- type 的取值改由 Go 侧约束：加一种通知不该需要一次 migration。
ALTER TABLE social_notifications DROP CONSTRAINT social_notifications_type_check;
ALTER TABLE social_notifications DROP CONSTRAINT social_notifications_check;

-- 去重键必须带上 recipient。关注通知的 user_movie_id 是 NULL，
-- 少了 recipient，「A 关注 B」和「A 关注 C」会塌成同一行。
DROP INDEX social_notifications_event_unique;
CREATE UNIQUE INDEX social_notifications_event_unique
    ON social_notifications (type, actor_user_id, recipient_user_id,
                             COALESCE(user_movie_id, 0), COALESCE(reply_id, 0));
