-- 热门发现允许保留尚未进入规范媒体库的外部榜单条目。
-- media_id 仅用于可选的播放质量加权，不再作为热门条目的身份主键。

ALTER TABLE popularity_snapshots
    DROP CONSTRAINT IF EXISTS popularity_snapshots_pkey;
ALTER TABLE popularity_snapshots
    DROP CONSTRAINT IF EXISTS popularity_snapshots_run_id_rank_key;
ALTER TABLE popularity_snapshots
    ALTER COLUMN media_id DROP NOT NULL;
ALTER TABLE popularity_snapshots
    ADD PRIMARY KEY (run_id, rank);

CREATE UNIQUE INDEX IF NOT EXISTS popularity_snapshots_run_media_uidx
    ON popularity_snapshots (run_id, media_id)
    WHERE media_id IS NOT NULL;
