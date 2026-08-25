-- 为影子候选提供稳定 API 身份，但不替换既有 source/vod/media 键。
-- 审计记录增加明确的变更前后身份，使后续绑定或拆分流程仍可解释。

ALTER TABLE resource_match_candidates
    ADD COLUMN IF NOT EXISTS id BIGSERIAL;
ALTER TABLE resource_match_candidates
    ADD COLUMN IF NOT EXISTS resolved_media_id BIGINT REFERENCES media(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS resource_match_candidates_id_uidx
    ON resource_match_candidates (id);

ALTER TABLE resource_match_audits
    ADD COLUMN IF NOT EXISTS candidate_id BIGINT REFERENCES resource_match_candidates(id) ON DELETE SET NULL;
ALTER TABLE resource_match_audits
    ADD COLUMN IF NOT EXISTS previous_media_id BIGINT;
ALTER TABLE resource_match_audits
    ADD COLUMN IF NOT EXISTS resolved_media_id BIGINT;

UPDATE resource_match_audits audit
SET candidate_id = candidate.id
FROM resource_match_candidates candidate
WHERE audit.candidate_id IS NULL
  AND candidate.source_key = audit.source_key
  AND candidate.vod_id = audit.vod_id
  AND candidate.media_id = audit.media_id;

UPDATE resource_match_audits
SET previous_media_id = media_id,
    resolved_media_id = CASE WHEN decision = 'verified' THEN media_id ELSE NULL END
WHERE previous_media_id IS NULL;

CREATE INDEX IF NOT EXISTS resource_match_audits_candidate_idx
    ON resource_match_audits (candidate_id, created_at DESC)
    WHERE candidate_id IS NOT NULL;
