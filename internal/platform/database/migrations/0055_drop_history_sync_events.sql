-- history_sync_events 已不再使用：
-- 客户端每次打开页面直接 GET 最新进度，不再需要增量同步账本。
DROP TABLE IF EXISTS history_sync_events;
