-- resource_match_audits 只有写入方，没有任何读取方：复核页和复核 API 读的都是
-- resource_match_candidates，没有任何界面能看到这张表。复核留痕改由一行结构化
-- 日志承担（internal/search/match_review.go），表和它的三个索引一起删掉。

DROP TABLE IF EXISTS resource_match_audits;
