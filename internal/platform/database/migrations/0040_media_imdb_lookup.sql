-- IMDb 映射从 tmdb 任务里拆出来，交给批量回填。
-- 光看「没有 imdb 外部 ID」无法区分「还没查过」和「查过但查不到」，
-- 不记录尝试时间的话，回填任务每一轮都会重新捞同一批查不到的条目，新条目永远排不上。
ALTER TABLE media ADD COLUMN imdb_lookup_at TIMESTAMPTZ;

CREATE INDEX media_imdb_lookup_idx ON media (imdb_lookup_at NULLS FIRST, id)
    WHERE douban_id <> '';
