-- 关联一次自动换源会话中的多次尝试。该结构为附加变更，
-- 保持现有 attempt/event 幂等键不变。

ALTER TABLE playback_attempt_events
    ADD COLUMN IF NOT EXISTS candidate_session_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS playback_attempt_events_session_idx
    ON playback_attempt_events (candidate_session_id, created_at)
    WHERE candidate_session_id <> '';

ALTER TABLE resource_lifecycle_batches
    ADD COLUMN IF NOT EXISTS applied_count INTEGER;
UPDATE resource_lifecycle_batches batch
SET applied_count = (
    SELECT COUNT(*) FROM vod_items resource WHERE resource.lifecycle_batch_id = batch.id
)
WHERE batch.status = 'applied' AND batch.applied_count IS NULL;
