-- 必须停止旧版本 Web/Worker 后执行；新代码不再引用候选 ID。
SET LOCAL lock_timeout = '3s';
DROP TABLE playback_attempt_events;
DROP TABLE resource_episode_candidates;
DROP TABLE resource_play_lines;
ALTER TABLE vod_items DROP COLUMN avg_speed_ms;
ALTER TABLE vod_items DROP COLUMN quality_refreshed_at;

-- 正片统一使用 feature 身份，保留现有单元 ID，历史引用不会因为改键丢失。
UPDATE playback_positions p SET season_number=0,episode_key='feature',episode='正片'
FROM media_units u WHERE p.media_unit_id=u.id AND u.unit_type='feature';

-- 清洗播放列表和重算可用性由 playbackreconcile 命令复用 Go 解析器完成，
-- 不在 SQL 中再实现第二套标签识别规则。首次发布须在开放流量前执行。
