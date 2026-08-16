package releaseaudit

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Row interface {
	Scan(destinations ...any) error
}

type Querier interface {
	QueryRow(ctx context.Context, query string, arguments ...any) Row
}

type Options struct {
	RequirePopularity         bool
	RequiredPopularitySources []string
	MaxPopularityAge          time.Duration
}

type Result struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Value       int64  `json:"value"`
	Description string `json:"description"`
}

type Summary struct {
	Checks       []Result `json:"checks"`
	Observations []Result `json:"observations"`
	Failed       int      `json:"failed"`
	Warnings     int      `json:"warnings"`
}

type checkSpec struct {
	name        string
	severity    string
	description string
	query       string
	arguments   []any
}

// Audit 只验证重构数据库必须满足的不变量，绝不会写入、修复、迁移数据或修改功能开关。
func Audit(ctx context.Context, database Querier, options Options) (Summary, error) {
	if database == nil {
		return Summary{}, fmt.Errorf("release audit database is not configured")
	}
	options = normalizeOptions(options)
	summary := Summary{Checks: make([]Result, 0), Observations: make([]Result, 0)}
	base := baseChecks()
	for index, spec := range base {
		result, err := run(ctx, database, spec, true)
		if err != nil {
			return Summary{}, err
		}
		summary.Checks = append(summary.Checks, result)
		countStatus(&summary, result)
		// 后续检查会引用最终表。migration 缺失本身已经是明确且可操作的发布阻断项，
		// 此时直接结束，避免继续产生大量“表不存在”的干扰错误。
		if index == 0 && result.Status == "fail" {
			return summary, nil
		}
	}
	if options.RequirePopularity {
		for _, spec := range popularityChecks(options) {
			result, err := run(ctx, database, spec, true)
			if err != nil {
				return Summary{}, err
			}
			summary.Checks = append(summary.Checks, result)
			countStatus(&summary, result)
		}
	}
	for _, spec := range observationSpecs() {
		result, err := run(ctx, database, spec, false)
		if err != nil {
			return Summary{}, err
		}
		summary.Observations = append(summary.Observations, result)
	}
	return summary, nil
}

func normalizeOptions(options Options) Options {
	if options.MaxPopularityAge <= 0 {
		options.MaxPopularityAge = 2 * time.Hour
	}
	if len(options.RequiredPopularitySources) == 0 {
		options.RequiredPopularitySources = []string{"douban", "tmdb", "activity"}
	}
	unique := make(map[string]bool)
	sources := make([]string, 0, len(options.RequiredPopularitySources))
	for _, source := range options.RequiredPopularitySources {
		source = strings.ToLower(strings.TrimSpace(source))
		if source == "" || unique[source] {
			continue
		}
		unique[source] = true
		sources = append(sources, source)
	}
	options.RequiredPopularitySources = sources
	return options
}

func run(ctx context.Context, database Querier, spec checkSpec, check bool) (Result, error) {
	var value int64
	if err := database.QueryRow(ctx, spec.query, spec.arguments...).Scan(&value); err != nil {
		return Result{}, fmt.Errorf("release audit %s: %w", spec.name, err)
	}
	status := "observed"
	if check {
		status = "pass"
		if value != 0 {
			status = spec.severity
		}
	}
	return Result{Name: spec.name, Status: status, Value: value, Description: spec.description}, nil
}

func countStatus(summary *Summary, result Result) {
	switch result.Status {
	case "fail":
		summary.Failed++
	case "warn":
		summary.Warnings++
	}
}

func baseChecks() []checkSpec {
	return []checkSpec{
		{name: "migration_0036_missing", severity: "fail", description: "the unified worker queue cutover must be applied",
			query: `SELECT CASE WHEN EXISTS (SELECT 1 FROM schema_migrations WHERE version = '0036') THEN 0 ELSE 1 END`},
		{name: "compatibility_table_present", severity: "fail", description: "retired compatibility tables must not remain in the final database",
			query: `SELECT COUNT(*) FROM (VALUES ('movies'),('watch_histories'),('resource_episodes'),('resource_playback_health'),('legacy_media_mappings'),('metadata_refresh_jobs'),('douban_sync_jobs')) retired(name) WHERE to_regclass('public.' || retired.name) IS NOT NULL`},
		{name: "compatibility_column_present", severity: "fail", description: "retired shadow columns must not remain in final tables",
			query: `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND ((table_name='vod_items' AND column_name IN ('avg_speed_ms','sample_count','failed_count')) OR (table_name='playback_positions' AND column_name IN ('legacy_history_id','legacy_payload')))`},
		{name: "duplicate_media_douban_id", severity: "fail", description: "canonical Douban IDs must remain unique",
			query: `SELECT COUNT(*) FROM (SELECT douban_id FROM media WHERE douban_id <> '' GROUP BY douban_id HAVING COUNT(*) > 1) duplicate`},
		{name: "user_movie_without_media", severity: "fail", description: "every wish or watched row must point to canonical media",
			query: `SELECT COUNT(*) FROM user_movies WHERE media_id IS NULL`},
		{name: "user_movie_identity_mismatch", severity: "fail", description: "public Douban path identity and canonical media identity must agree",
			query: `SELECT COUNT(*) FROM user_movies user_movie JOIN media ON media.id=user_movie.media_id WHERE user_movie.movie_id IS DISTINCT FROM media.douban_id`},
		{name: "resource_link_orphan", severity: "fail", description: "resource links must resolve to both a resource and canonical media",
			query: `SELECT COUNT(*) FROM resource_media_links link LEFT JOIN vod_items resource ON resource.source_key = link.source_key AND resource.vod_id = link.vod_id LEFT JOIN media ON media.id = link.media_id WHERE resource.source_key IS NULL OR media.id IS NULL`},
		{name: "linkable_resource_unmapped", severity: "warn", description: "resources with an existing canonical Douban ID should already be linked",
			query: `SELECT COUNT(*) FROM vod_items resource JOIN media ON media.douban_id = resource.vod_douban_id WHERE resource.vod_douban_id <> '' AND NOT EXISTS (SELECT 1 FROM resource_media_links link WHERE link.source_key = resource.source_key AND link.vod_id = resource.vod_id)`},
		{name: "candidate_identity_mismatch", severity: "fail", description: "episode candidates and media units must belong to the same media",
			query: `SELECT COUNT(*) FROM resource_episode_candidates candidate JOIN media_units unit ON unit.id = candidate.media_unit_id WHERE candidate.media_id IS DISTINCT FROM unit.media_id`},
		{name: "duplicate_active_playback_position", severity: "fail", description: "one user and canonical episode identity must not produce duplicate active cards",
			query: `SELECT COUNT(*) FROM (SELECT user_id, media_id, season_number, episode_key FROM playback_positions WHERE deleted_at IS NULL AND media_id IS NOT NULL GROUP BY user_id, media_id, season_number, episode_key HAVING COUNT(*) > 1) duplicate`},
		{name: "playback_rollup_identity_mismatch", severity: "fail", description: "quality rollups must remain bound to the exact candidate and unit",
			query: `SELECT COUNT(*) FROM playback_quality_rollups quality JOIN resource_episode_candidates candidate ON candidate.id = quality.candidate_id WHERE quality.media_id IS DISTINCT FROM candidate.media_id OR quality.media_unit_id IS DISTINCT FROM candidate.media_unit_id OR quality.play_line_id IS DISTINCT FROM candidate.line_id`},
		{name: "playback_quality_invalid_total", severity: "fail", description: "candidate success and failure totals cannot be negative or exceed attempts",
			query: `SELECT COUNT(*) FROM (SELECT candidate_id, SUM(attempt_count) attempts, SUM(success_count) successes, SUM(failure_count) failures FROM playback_quality_rollups GROUP BY candidate_id) quality WHERE attempts < 0 OR successes < 0 OR failures < 0 OR successes > attempts OR failures > attempts`},
		{name: "uncorrelated_recent_source_switch", severity: "warn", description: "new automatic source switches should include a candidate session ID",
			query: `SELECT COUNT(*) FROM playback_attempt_events WHERE event_type = 'source_switched' AND candidate_session_id = '' AND created_at >= NOW() - INTERVAL '24 hours'`},
		{name: "wrong_unit_failover_session", severity: "fail", description: "one failover session must never cross media units",
			query: `SELECT COUNT(*) FROM (SELECT candidate_session_id FROM playback_attempt_events WHERE candidate_session_id <> '' GROUP BY candidate_session_id HAVING COUNT(DISTINCT media_unit_id) > 1) invalid`},
		{name: "cold_without_timestamp", severity: "fail", description: "cold resources must retain an auditable transition timestamp",
			query: `SELECT COUNT(*) FROM vod_items WHERE resource_status = 'cold' AND cold_at IS NULL`},
		{name: "active_with_lifecycle_batch", severity: "fail", description: "reactivated resources must clear their cooling batch reference",
			query: `SELECT COUNT(*) FROM vod_items WHERE resource_status = 'active' AND lifecycle_batch_id IS NOT NULL`},
		{name: "lifecycle_batch_item_mismatch", severity: "fail", description: "frozen dry-run batch count must equal its immutable item set",
			query: `SELECT COUNT(*) FROM resource_lifecycle_batches batch LEFT JOIN (SELECT batch_id, COUNT(*) item_count FROM resource_lifecycle_batch_items GROUP BY batch_id) item ON item.batch_id = batch.id WHERE batch.candidate_count <> COALESCE(item.item_count, 0)`},
		{name: "applied_lifecycle_count_missing", severity: "fail", description: "applied cooling batches must retain the exact affected row count",
			query: `SELECT COUNT(*) FROM resource_lifecycle_batches WHERE status = 'applied' AND applied_count IS NULL`},
		{name: "expired_unapplied_lifecycle_batch", severity: "warn", description: "expired previews should be recreated before apply",
			query: `SELECT COUNT(*) FROM resource_lifecycle_batches WHERE status = 'previewed' AND expires_at <= NOW()`},
		{name: "running_refresh_without_lease", severity: "warn", description: "running refresh jobs should always have an active database lease",
			query: `SELECT COUNT(*) FROM worker_jobs WHERE status = 'running' AND (locked_by = '' OR locked_until IS NULL)`},
	}
}

func popularityChecks(options Options) []checkSpec {
	seconds := int64(options.MaxPopularityAge / time.Second)
	return []checkSpec{
		{name: "missing_ready_popularity_category", severity: "fail", description: "every discovery category needs a ready, non-expired snapshot",
			query: `SELECT COUNT(*) FROM (VALUES ('movie'),('tv'),('show'),('cartoon')) category(media_type) WHERE NOT EXISTS (SELECT 1 FROM popularity_snapshot_runs run WHERE run.media_type = category.media_type AND run.status = 'ready' AND run.expires_at > NOW())`},
		{name: "stale_popularity_category", severity: "fail", description: "latest ready popularity snapshots must be recent enough for cutover",
			query: `SELECT COUNT(*) FROM (SELECT DISTINCT ON (media_type) media_type, generated_at FROM popularity_snapshot_runs WHERE status = 'ready' ORDER BY media_type, generated_at DESC) latest WHERE latest.generated_at < NOW() - make_interval(secs => $1)`, arguments: []any{seconds}},
		{name: "missing_popularity_source", severity: "fail", description: "latest snapshots must explain every required source contribution",
			query: `SELECT COUNT(*) FROM (SELECT DISTINCT ON (media_type) media_type, source_status FROM popularity_snapshot_runs WHERE status = 'ready' ORDER BY media_type, generated_at DESC) latest CROSS JOIN UNNEST($1::text[]) source(name) WHERE NOT (latest.source_status ? source.name) OR COALESCE((latest.source_status ->> source.name)::integer, 0) = 0`, arguments: []any{options.RequiredPopularitySources}},
		{name: "incomplete_ready_popularity_snapshot", severity: "fail", description: "every published popularity run must contain exactly 50 items",
			query: `SELECT COUNT(*) FROM (SELECT DISTINCT ON (media_type) media_type, item_count FROM popularity_snapshot_runs WHERE status = 'ready' ORDER BY media_type, generated_at DESC) latest WHERE latest.item_count <> 50`},
	}
}

func observationSpecs() []checkSpec {
	return []checkSpec{
		{name: "media_total", description: "canonical media rows", query: `SELECT COUNT(*) FROM media`},
		{name: "user_movie_total", description: "wish and watched rows", query: `SELECT COUNT(*) FROM user_movies`},
		{name: "resource_total", description: "searchable resource rows", query: `SELECT COUNT(*) FROM vod_items`},
		{name: "resource_linked_total", description: "resources mapped to canonical media", query: `SELECT COUNT(*) FROM resource_media_links`},
		{name: "playback_position_total", description: "playback positions including tombstones", query: `SELECT COUNT(*) FROM playback_positions`},
		{name: "active_playback_position_total", description: "visible playback history rows", query: `SELECT COUNT(*) FROM playback_positions WHERE deleted_at IS NULL`},
		{name: "playback_event_total", description: "idempotent playback quality events", query: `SELECT COUNT(*) FROM playback_attempt_events`},
		{name: "cold_resource_total", description: "recoverably cooled resources", query: `SELECT COUNT(*) FROM vod_items WHERE resource_status = 'cold'`},
		{name: "pending_refresh_total", description: "pending worker jobs", query: `SELECT COUNT(*) FROM worker_jobs WHERE status = 'pending'`},
		{name: "ready_popularity_run_total", description: "published popularity snapshot runs", query: `SELECT COUNT(*) FROM popularity_snapshot_runs WHERE status = 'ready'`},
	}
}
