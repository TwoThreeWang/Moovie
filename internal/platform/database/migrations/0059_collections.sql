-- 片单。owner_user_id 为 NULL 表示官方精选，非 NULL 表示某个用户自建，
-- 两者共用一张表：开放用户片单时不需要再建一套结构，也不需要搬数据。
-- featured 与归属解耦：官方片单可以先建后发，优质用户片单可以直接提权。
CREATE TABLE collections (
    id            bigserial PRIMARY KEY,
    owner_user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    slug          text NOT NULL UNIQUE,
    title         text NOT NULL,
    description   text NOT NULL DEFAULT '',
    featured      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT NOW(),
    updated_at    timestamptz NOT NULL DEFAULT NOW()
);

-- 只有 featured 的片单进发现流和 sitemap，这个索引服务的就是那条查询。
CREATE INDEX collections_featured_idx ON collections (updated_at DESC) WHERE featured;
CREATE INDEX collections_owner_idx ON collections (owner_user_id, updated_at DESC);

-- 条目挂 media_id 而不是豆瓣 ID：media 是已经洗干净的身份层，
-- 挂上去才能和资源、播放候选、相似推荐打通。
-- note 是每条的推荐语，也是片单和「收藏夹」的分界线：
-- 没有推荐语的片单只是个列表，有推荐语的才是内容。
CREATE TABLE collection_items (
    collection_id bigint NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    media_id      bigint NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position      int NOT NULL,
    note          text NOT NULL DEFAULT '',
    PRIMARY KEY (collection_id, media_id)
);

CREATE INDEX collection_items_order_idx ON collection_items (collection_id, position);
