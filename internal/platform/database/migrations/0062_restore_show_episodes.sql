-- 0061 按 media.media_type = 'movie' 把候选折叠成正片，但豆瓣按 movie/tv/show 顺序试端点、
-- 第一个成功的即为准，综艺常被错标成 movie，于是整季被并进了一个 S01E01。
-- 解析器现在要求标签本身也是清晰度/版本名（isQualityVariantLabel）才折叠，
-- 这里把已经折叠错的行删掉：候选是从 vod_items.vod_play_url 解析出来的派生数据，
-- 源数据没动，下次索引（worker、/play，或 /watch 查不到候选时的现场补录）会用修正后的逻辑重建。

DELETE FROM resource_episode_candidates
WHERE season_number = 1
  AND episode_key = 'S01E01'
  AND quality <> ''
  -- 0061 把原来的集名存进了 quality，据此认出集次：带集/期/话的，
  -- 以及 20240315、2024-03-15 这类按日期编号的综艺。
  AND (quality ~ '[集期話话]'
       OR quality ~ '^[0-9]{5,}$'
       OR quality ~ '^[0-9]{4}[-./][0-9]{1,2}[-./][0-9]{1,2}$');
