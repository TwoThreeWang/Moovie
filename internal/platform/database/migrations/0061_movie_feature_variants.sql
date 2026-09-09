-- 电影的清晰度/版本（720P、HD中字、TC国语、抢先版…）此前被当成不同的"集"写进候选表，
-- 选集网格因此变成一排各家叫法不一的清晰度按钮。现在它们统一折叠成一个正片单元
-- （season_number = 1, episode_key = 'S01E01'），版本差异改存 quality，
-- 在线路列表里作为版本标签展示。唯一键必须跟着带上 quality，否则同一条线路的
-- 多个版本会互相覆盖，只剩最后写入的那一条。

-- 1. 先放开旧唯一约束，否则下面的回填会在同线路第二行就冲突。
--    约束名是 Postgres 自动生成的，长度超限后会被截断，按名字写死不可靠，
--    因此扫 pg_constraint 把这张表上除新键以外的唯一约束都摘掉。
DO $$
DECLARE stale RECORD;
BEGIN
    FOR stale IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'resource_episode_candidates'::regclass
          AND contype = 'u'
          AND conname <> 'resource_episode_candidates_line_episode_quality_key'
    LOOP
        EXECUTE format('ALTER TABLE resource_episode_candidates DROP CONSTRAINT %I', stale.conname);
    END LOOP;
END $$;

-- 2. 回填：电影候选归到正片键，原来的"集名"降级成版本标签。
UPDATE resource_episode_candidates candidate
SET season_number = 1,
    episode_key = 'S01E01',
    quality = CASE WHEN candidate.quality = '' THEN candidate.episode_label ELSE candidate.quality END,
    updated_at = NOW()
FROM media
WHERE candidate.media_id = media.id
  AND media.media_type = 'movie'
  AND (candidate.season_number <> 1 OR candidate.episode_key <> 'S01E01' OR candidate.quality = '');

-- 回填不会撞上第 4 步的新唯一键：quality 在此之前从未被写入过（一直是空串），
-- 折叠后的版本标签就是 episode_label，而同一条线路里的 episode_key 各不相同，
-- 相同的标签本来就归一成同一个 key，早被旧唯一键合并掉了。

-- 3. 把这些候选重新挂到电影的 feature 单元上（EnsureMediaUnit 对 feature 固定用 0/'feature'），
--    否则播放统计仍然记在按清晰度拆出来的旧单元上。
INSERT INTO media_units (media_id, unit_type, season_number, episode_key, title)
SELECT DISTINCT candidate.media_id, 'feature', 0, 'feature', ''
FROM resource_episode_candidates candidate
JOIN media ON media.id = candidate.media_id
WHERE media.media_type = 'movie'
ON CONFLICT (media_id, unit_type, season_number, episode_key) DO NOTHING;

UPDATE resource_episode_candidates candidate
SET media_unit_id = unit.id
FROM media
JOIN media_units unit ON unit.media_id = media.id
 AND unit.unit_type = 'feature' AND unit.season_number = 0 AND unit.episode_key = 'feature'
WHERE candidate.media_id = media.id
  AND media.media_type = 'movie'
  AND candidate.media_unit_id IS DISTINCT FROM unit.id;

-- 4. 新唯一键：同一条线路里，一个集次可以有多个版本。
ALTER TABLE resource_episode_candidates
    ADD CONSTRAINT resource_episode_candidates_line_episode_quality_key
    UNIQUE (line_id, season_number, episode_key, quality);
