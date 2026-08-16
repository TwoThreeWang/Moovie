package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

var (
	ErrEmptyPopularitySnapshot      = errors.New("popularity snapshot has no items")
	ErrIncompletePopularitySnapshot = errors.New("popularity snapshot has fewer than 50 items")
)

const popularitySnapshotSize = 50

type popularitySnapshotDatabase interface {
	database.Executor
	database.Beginner
}

type popularitySignal struct {
	mediaID    int64
	doubanID   string
	year       string
	candidates int64
	attempts   int64
	successes  int64
}

type rankedPopularitySubject struct {
	subject PopularSubject
	mediaID any
}

// PopularitySnapshotStore 保存不可变的热门快照；media_id 只在能够匹配规范媒体时填写。
// 只有同一事务写完全部条目并将批次标记为 ready 后，该批次才对读取方可见。
type PopularitySnapshotStore struct {
	database popularitySnapshotDatabase
}

func NewPopularitySnapshotStore(db popularitySnapshotDatabase) *PopularitySnapshotStore {
	return &PopularitySnapshotStore{database: db}
}

func (store *PopularitySnapshotStore) Replace(ctx context.Context, mediaType string, subjects []PopularSubject, ttl time.Duration) error {
	if store == nil || store.database == nil {
		return fmt.Errorf("popularity snapshot database is not configured")
	}
	if _, err := activityMediaType(mediaType); err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("popularity snapshot ttl must be positive")
	}
	if len(subjects) == 0 {
		return fmt.Errorf("%w for media type %q: source returned no subjects", ErrEmptyPopularitySnapshot, mediaType)
	}
	ids := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		if id := strings.TrimSpace(subject.ID); id != "" {
			ids = append(ids, id)
		}
	}
	signals, err := store.loadSignals(ctx, ids)
	if err != nil {
		return err
	}
	ranked := rankPopularitySubjects(subjects, signals, time.Now())
	if len(ranked) == 0 {
		return fmt.Errorf("%w for media type %q", ErrEmptyPopularitySnapshot, mediaType)
	}
	if len(ranked) < popularitySnapshotSize {
		return fmt.Errorf("%w for media type %q: got %d", ErrIncompletePopularitySnapshot, mediaType, len(ranked))
	}

	sourceCounts := make(map[string]int)
	for _, item := range ranked {
		for source := range item.subject.SourceRanks {
			sourceCounts[source]++
		}
	}
	sourceStatus, err := json.Marshal(sourceCounts)
	if err != nil {
		return fmt.Errorf("encode popularity source status: %w", err)
	}
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin popularity snapshot: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))
	var runID int64
	if err := transaction.QueryRow(ctx, `INSERT INTO popularity_snapshot_runs
(media_type, status, source_status, item_count, generated_at, expires_at)
VALUES ($1, 'building', $2::jsonb, 0, NOW(), NOW() + $3::interval)
RETURNING id`, mediaType, string(sourceStatus), intervalLiteral(ttl)).Scan(&runID); err != nil {
		return fmt.Errorf("create popularity snapshot run: %w", err)
	}
	for index, item := range ranked {
		payload, encodeErr := json.Marshal(item.subject)
		if encodeErr != nil {
			return fmt.Errorf("encode popularity subject: %w", encodeErr)
		}
		ranks, encodeErr := json.Marshal(item.subject.SourceRanks)
		if encodeErr != nil {
			return fmt.Errorf("encode popularity source ranks: %w", encodeErr)
		}
		rrfScore := 0.0
		if item.subject.QualityMultiplier > 0 {
			rrfScore = (item.subject.Score - item.subject.FreshnessBoost) / item.subject.QualityMultiplier
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO popularity_snapshots
(run_id, media_id, rank, rrf_score, final_score, source_ranks, subject_payload,
 playable_candidate_count, quality_multiplier, freshness_boost, generated_at)
VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9,$10,NOW())`,
			runID, item.mediaID, index+1, rrfScore, item.subject.Score, string(ranks), string(payload),
			item.subject.PlayableCandidates, item.subject.QualityMultiplier, item.subject.FreshnessBoost); err != nil {
			return fmt.Errorf("insert popularity snapshot item: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `UPDATE popularity_snapshot_runs
SET status = 'ready', item_count = $2, completed_at = NOW()
WHERE id = $1 AND status = 'building'`, runID, len(ranked)); err != nil {
		return fmt.Errorf("publish popularity snapshot: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit popularity snapshot: %w", err)
	}
	return nil
}

func rankPopularitySubjects(subjects []PopularSubject, signals map[string]popularitySignal, now time.Time) []rankedPopularitySubject {
	ranked := make([]rankedPopularitySubject, 0, len(subjects))
	for _, subject := range subjects {
		signal, found := signals[strings.TrimSpace(subject.ID)]
		var mediaID any
		subject.QualityMultiplier = 1
		year := subject.Year
		if found {
			mediaID = signal.mediaID
			year = signal.year
			subject.PlayableCandidates = int(signal.candidates)
			subject.QualityMultiplier = qualityMultiplier(signal)
		}
		subject.FreshnessBoost = freshnessBoost(year, now)
		subject.Score = subject.Score*subject.QualityMultiplier + subject.FreshnessBoost
		ranked = append(ranked, rankedPopularitySubject{subject: subject, mediaID: mediaID})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].subject.Score == ranked[j].subject.Score {
			return ranked[i].subject.Title < ranked[j].subject.Title
		}
		return ranked[i].subject.Score > ranked[j].subject.Score
	})
	if len(ranked) > popularitySnapshotSize {
		ranked = ranked[:popularitySnapshotSize]
	}
	return ranked
}

func (store *PopularitySnapshotStore) loadSignals(ctx context.Context, doubanIDs []string) (map[string]popularitySignal, error) {
	result := make(map[string]popularitySignal)
	if len(doubanIDs) == 0 {
		return result, nil
	}
	rows, err := store.database.Query(ctx, `SELECT media.id, media.douban_id, media.year,
       COALESCE(candidate.playable_count, 0), COALESCE(quality.attempts, 0), COALESCE(quality.successes, 0)
FROM media
LEFT JOIN LATERAL (
    SELECT COUNT(DISTINCT episode.id) AS playable_count
    FROM resource_episode_candidates episode
    JOIN resource_play_lines line ON line.id = episode.line_id
    WHERE episode.media_id = media.id
      AND episode.resource_status IN ('active', 'cold')
      AND line.resource_status IN ('active', 'cold')
) candidate ON TRUE
LEFT JOIN LATERAL (
    SELECT SUM(rollup.attempt_count) AS attempts, SUM(rollup.success_count) AS successes
    FROM playback_quality_rollups rollup
    WHERE rollup.media_id = media.id AND rollup.bucket >= NOW() - INTERVAL '7 days'
) quality ON TRUE
WHERE media.douban_id = ANY($1::text[])`, doubanIDs)
	if err != nil {
		return nil, fmt.Errorf("query popularity quality signals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var signal popularitySignal
		if err := rows.Scan(&signal.mediaID, &signal.doubanID, &signal.year, &signal.candidates, &signal.attempts, &signal.successes); err != nil {
			return nil, fmt.Errorf("scan popularity quality signal: %w", err)
		}
		result[signal.doubanID] = signal
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate popularity quality signals: %w", err)
	}
	return result, nil
}

func (store *PopularitySnapshotStore) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	if store == nil || store.database == nil {
		return nil, fmt.Errorf("popularity snapshot database is not configured")
	}
	rows, err := store.database.Query(ctx, `SELECT snapshot.subject_payload,
COALESCE(media.douban_id, ''), COALESCE(media.title, ''), COALESCE(media.year, ''),
COALESCE(media.poster, ''), COALESCE(media.rating_douban, 0)
FROM popularity_snapshot_runs run
JOIN popularity_snapshots snapshot ON snapshot.run_id = run.id
LEFT JOIN media ON media.id = snapshot.media_id
WHERE run.id = (
    SELECT latest.id FROM popularity_snapshot_runs latest
    WHERE latest.media_type = $1 AND latest.status = 'ready' AND latest.expires_at > NOW()
    ORDER BY latest.generated_at DESC LIMIT 1
)
ORDER BY snapshot.rank`, mediaType)
	if err != nil {
		return nil, fmt.Errorf("query popularity snapshot: %w", err)
	}
	defer rows.Close()
	items := make([]PopularSubject, 0)
	for rows.Next() {
		var payload []byte
		var doubanID, title, year, poster string
		var rating float64
		if err := rows.Scan(&payload, &doubanID, &title, &year, &poster, &rating); err != nil {
			return nil, fmt.Errorf("scan popularity snapshot: %w", err)
		}
		var subject PopularSubject
		if err := json.Unmarshal(payload, &subject); err != nil {
			return nil, fmt.Errorf("decode popularity snapshot: %w", err)
		}
		if doubanID != "" {
			subject.ID = doubanID
		}
		if title != "" {
			subject.Title = title
		}
		if year != "" {
			subject.Year = year
		}
		if poster != "" {
			subject.Cover = proxyImagePath(poster)
		}
		if rating > 0 {
			subject.Rate = strconv.FormatFloat(rating, 'f', 1, 64)
		}
		items = append(items, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate popularity snapshot: %w", err)
	}
	return items, nil
}

type SnapshotPopularProvider struct {
	snapshots *PopularitySnapshotStore
	fallback  PopularProvider
}

func NewSnapshotPopularProvider(snapshots *PopularitySnapshotStore, fallback PopularProvider) *SnapshotPopularProvider {
	return &SnapshotPopularProvider{snapshots: snapshots, fallback: fallback}
}

func (provider *SnapshotPopularProvider) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	items, err := provider.snapshots.Popular(ctx, mediaType)
	if len(items) >= popularitySnapshotSize {
		return items[:popularitySnapshotSize], nil
	}
	if provider.fallback == nil {
		return items, err
	}
	fallback, fallbackErr := provider.fallback.Popular(ctx, mediaType)
	items = mergePopularSubjects(items, fallback, popularitySnapshotSize)
	if len(items) > 0 {
		return items, nil
	}
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	return nil, err
}

func mergePopularSubjects(primary, supplement []PopularSubject, limit int) []PopularSubject {
	if limit <= 0 {
		return nil
	}
	result := make([]PopularSubject, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, subjects := range [][]PopularSubject{primary, supplement} {
		for _, subject := range subjects {
			identity := popularIdentity(subject)
			if identity == "" {
				continue
			}
			if _, found := seen[identity]; found {
				continue
			}
			seen[identity] = struct{}{}
			result = append(result, subject)
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

const TaskPopularityRefresh = "popularity_refresh"

type PopularityRefresher struct {
	store    *PopularitySnapshotStore
	provider PopularProvider
	ttl      time.Duration
}

func NewPopularityRefresher(store *PopularitySnapshotStore, provider PopularProvider, interval time.Duration) *PopularityRefresher {
	return &PopularityRefresher{store: store, provider: provider, ttl: 2 * interval}
}

func (refresher *PopularityRefresher) Handle(ctx context.Context, _ workqueue.Job) error {
	if refresher == nil || refresher.store == nil || refresher.provider == nil {
		return fmt.Errorf("popularity refresher is not configured")
	}
	var failures []error
	for _, mediaType := range []string{"movie", "tv", "show", "cartoon"} {
		items, err := refresher.provider.Popular(ctx, mediaType)
		if err != nil {
			slog.Warn("popularity source refresh failed", "media_type", mediaType, "error", err)
			failures = append(failures, err)
			continue
		}
		if err := refresher.store.Replace(ctx, mediaType, items, refresher.ttl); err != nil {
			slog.Warn("popularity snapshot publish failed", "media_type", mediaType, "error", err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func qualityMultiplier(signal popularitySignal) float64 {
	if signal.attempts < 5 {
		return 1
	}
	if float64(signal.successes)/float64(signal.attempts) < 0.2 {
		return 0.7
	}
	return 1
}

func freshnessBoost(year string, now time.Time) float64 {
	year = strings.TrimSpace(year)
	if len(year) > 4 {
		year = year[:4]
	}
	value, err := strconv.Atoi(year)
	if err != nil || value < now.Year() {
		return 0
	}
	return 0.0005
}

func intervalLiteral(duration time.Duration) string {
	return strconv.FormatInt(int64(duration/time.Second), 10) + " seconds"
}
