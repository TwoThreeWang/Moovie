-- 0042_worker_job_enqueue_cooldown.sql：给「详情页自动入队」的冷却判断加索引。
-- worker_jobs 原本只有 (task_type, subject_key) 在 pending/running 上的唯一偏索引，
-- 它只保证同一个对象同时只有一个任务，挡不住「任务刚结束就立刻再排一个」。
-- 详情页的几个入队条件（资料不全、剧照为空、短评为空、向量为空）都挂在
-- 「某个字段还是空的」上，而那个字段只有抓成功才会被填上；上游一坏、
-- 或者上游本来就没有这份数据（TMDB 大量条目没有剧照、冷门片没有短评），
-- 条件就永远成立，页面每被访问一次就重新入队一次，上游越不稳打得越狠。
-- 冷却要按 (task_type, subject_key) 查最近一次终态任务的结束时间，
-- 现有三个索引都只覆盖 pending 或 (status, updated_at)，不加这个会退化成全表扫。
CREATE INDEX worker_jobs_cooldown_idx
    ON worker_jobs (task_type, subject_key, finished_at DESC)
    WHERE status IN ('completed', 'failed');
