-- 编辑指定的单向关联；位置限制同时保证每个片单最多三项。
CREATE TABLE collection_relations (
    collection_id bigint NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    related_id bigint NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    position int NOT NULL CHECK (position BETWEEN 1 AND 3),
    reason text NOT NULL DEFAULT '',
    PRIMARY KEY (collection_id, related_id),
    UNIQUE (collection_id, position),
    CHECK (collection_id <> related_id)
);
CREATE INDEX collection_relations_target_idx ON collection_relations (related_id);
