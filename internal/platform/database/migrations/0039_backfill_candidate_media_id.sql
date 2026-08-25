-- 回填 resource_episode_candidates.media_id。
--
-- 背景：剧集候选在 search.persistItem 中写入，而媒体身份匹配在其后才执行，
-- 因此写入时 item.MediaID 恒为 0，media_id 落库为 NULL。
-- enrichMediaIdentity 第 0 层命中已有关联时直接返回，不会调用 LinkResource，
-- 也就不会触发 LinkResource 内部的候选回填 UPDATE。
--
-- 后果：resource_media_links 里关联齐全（搜索页显示"N 个已确认资源"），
-- 但 /watch/:douban_id 按 media_id 查 resource_episode_candidates 得到 0 行，
-- 于是 302 跳回 /search，用户看到"有资源却打不开"。
--
-- 代码侧已在 IndexResourceEpisodes 中补齐关联，此处修复存量数据。

UPDATE resource_episode_candidates candidate
SET media_id = link.media_id,
    updated_at = NOW()
FROM resource_play_lines line
JOIN resource_media_links link
  ON link.source_key = line.source_key AND link.vod_id = line.vod_id
WHERE candidate.line_id = line.id
  AND candidate.media_id IS NULL
  AND link.media_id IS NOT NULL;
