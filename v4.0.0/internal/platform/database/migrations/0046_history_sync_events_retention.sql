-- history_sync_events 是只追加账本，此前既没有清理，也没有支撑
-- bootstrapSyncEvents 那条 NOT EXISTS 的索引——用户越久，每次同步越慢。

-- bootstrapSyncEvents 按 (user_id, record_id, conflict_reason='') 反查
-- 「这条进度有没有成功事件」。没有这个索引时只能用 user_id 前缀，
-- 于是每条进度都要扫一遍该用户的整本账本：进度数 × 事件数。
CREATE INDEX IF NOT EXISTS history_sync_events_bootstrap_idx
    ON history_sync_events (user_id, record_id)
    WHERE conflict_reason = '' AND record_id IS NOT NULL;

-- 保留期清理按 created_at 取最旧的一批，没有这个索引就是全表扫。
CREATE INDEX IF NOT EXISTS history_sync_events_created_at_idx
    ON history_sync_events (created_at);
