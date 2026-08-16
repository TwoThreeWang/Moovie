-- 低置信度标题匹配只是待复核证据，不是规范关联。
-- 候选保存在独立影子表中，使既有 resource_media_links 保持可回滚，
-- 也避免把未确认候选展示成已确认身份。

CREATE TABLE IF NOT EXISTS resource_match_candidates (
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    confidence NUMERIC(5,4) NOT NULL,
    match_method TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'review' CHECK (status IN ('review', 'verified', 'rejected')),
    reason JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_key, vod_id, media_id),
    FOREIGN KEY (source_key, vod_id) REFERENCES vod_items(source_key, vod_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS resource_match_candidates_review_idx
    ON resource_match_candidates (status, confidence DESC, updated_at DESC);

INSERT INTO resource_match_candidates
(source_key, vod_id, media_id, confidence, match_method, status, reason)
SELECT source_key, vod_id, media_id, confidence, matched_by, 'review',
       jsonb_build_object('migrated_from_resource_link', TRUE)
FROM resource_media_links
WHERE confidence >= 0.68 AND confidence < 0.88
  AND is_locked = FALSE AND verified_at IS NULL
ON CONFLICT (source_key, vod_id, media_id) DO UPDATE SET
    confidence = EXCLUDED.confidence,
    match_method = EXCLUDED.match_method,
    status = 'review',
    reason = EXCLUDED.reason,
    updated_at = NOW();
