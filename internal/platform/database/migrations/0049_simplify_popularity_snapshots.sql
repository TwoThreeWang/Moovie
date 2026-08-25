ALTER TABLE popularity_snapshots
    DROP COLUMN IF EXISTS rrf_score,
    DROP COLUMN IF EXISTS final_score,
    DROP COLUMN IF EXISTS source_ranks,
    DROP COLUMN IF EXISTS playable_candidate_count,
    DROP COLUMN IF EXISTS quality_multiplier,
    DROP COLUMN IF EXISTS freshness_boost;
