package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
)

// 匹配候选的三种状态：待复核、已确认、已否决。
const (
	MatchStatusReview   = "review"
	MatchStatusVerified = "verified"
	MatchStatusRejected = "rejected"
)

// MatchCandidate 是一条待人工复核的“资源↔媒体”匹配候选，同时带上两边的展示信息便于后台比对。
type MatchCandidate struct {
	ID               int64   `json:"id"`
	SourceKey        string  `json:"source_key"`
	VodID            string  `json:"vod_id"`
	MediaID          int     `json:"media_id"`
	ResolvedMediaID  int     `json:"resolved_media_id,omitempty"`
	Confidence       float64 `json:"confidence"`
	MatchMethod      string  `json:"match_method"`
	Status           string  `json:"status"`
	ReasonJSON       string  `json:"-"`
	ResourceTitle    string  `json:"resource_title"`
	ResourceYear     string  `json:"resource_year"`
	ResourcePoster   string  `json:"resource_poster"`
	ResourceActors   string  `json:"resource_actors"`
	ResourceDirector string  `json:"resource_director"`
	MediaTitle       string  `json:"media_title"`
	MediaYear        string  `json:"media_year"`
	MediaPoster      string  `json:"media_poster"`
	MediaType        string  `json:"media_type"`
}

// MarshalJSON 额外把打分理由（reason）作为对象输出，而不是一个 JSON 字符串。
func (candidate MatchCandidate) MarshalJSON() ([]byte, error) {
	type candidateAlias MatchCandidate
	reason := any(map[string]any{})
	if candidate.ReasonJSON != "" {
		if err := json.Unmarshal([]byte(candidate.ReasonJSON), &reason); err != nil {
			reason = map[string]any{}
		}
	}
	return json.Marshal(struct {
		candidateAlias
		Reason any `json:"reason"`
	}{candidateAlias: candidateAlias(candidate), Reason: reason})
}

// MatchReviewStore 是后台复核匹配候选所需的存储接口。
type MatchReviewStore interface {
	ListMatchCandidates(ctx context.Context, status string, limit int) ([]MatchCandidate, error)
	ReviewMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID, actorUserID int, decision, reason string) error
	ReviewMatchCandidateByID(ctx context.Context, candidateID int64, actorUserID int, decision, reason string) error
	ResolveMatchCandidateByID(ctx context.Context, candidateID int64, resolvedMediaID, actorUserID int, decision, reason string) error
}

// ListMatchCandidates 按状态列出候选，置信度高的排前面。
func (store *PostgresStore) ListMatchCandidates(ctx context.Context, status string, limit int) ([]MatchCandidate, error) {
	status = normalizeMatchDecision(status, MatchStatusReview)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.database.Query(ctx, `SELECT candidate.id, candidate.source_key, candidate.vod_id, candidate.media_id,
COALESCE(candidate.resolved_media_id, 0),
candidate.confidence, candidate.match_method, candidate.status, candidate.reason::text,
resource.vod_name, resource.vod_year, resource.vod_pic, resource.vod_actor, resource.vod_director,
media.title, media.year, media.poster, media.media_type
FROM resource_match_candidates candidate
JOIN vod_items resource ON resource.source_key = candidate.source_key AND resource.vod_id = candidate.vod_id
JOIN media ON media.id = candidate.media_id
WHERE candidate.status = $1
ORDER BY candidate.confidence DESC, candidate.updated_at ASC
LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list resource match candidates: %w", err)
	}
	defer rows.Close()
	result := make([]MatchCandidate, 0)
	for rows.Next() {
		var candidate MatchCandidate
		if err := rows.Scan(&candidate.ID, &candidate.SourceKey, &candidate.VodID, &candidate.MediaID, &candidate.ResolvedMediaID,
			&candidate.Confidence, &candidate.MatchMethod, &candidate.Status, &candidate.ReasonJSON,
			&candidate.ResourceTitle, &candidate.ResourceYear, &candidate.ResourcePoster,
			&candidate.ResourceActors, &candidate.ResourceDirector, &candidate.MediaTitle,
			&candidate.MediaYear, &candidate.MediaPoster, &candidate.MediaType); err != nil {
			return nil, fmt.Errorf("scan resource match candidate: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource match candidates: %w", err)
	}
	return result, nil
}

// ReviewMatchCandidate 按 (source_key, vod_id, media_id) 定位候选并做出复核决定。
func (store *PostgresStore) ReviewMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID, actorUserID int, decision, reason string) error {
	sourceKey, vodID, reason = strings.TrimSpace(sourceKey), strings.TrimSpace(vodID), strings.TrimSpace(reason)
	if sourceKey == "" || vodID == "" || mediaID <= 0 {
		return errors.New("invalid resource match review")
	}
	return store.reviewMatchCandidate(ctx, 0, sourceKey, vodID, mediaID, 0, actorUserID, decision, reason)
}

// ReviewMatchCandidateByID 按候选 ID 复核，沿用候选自带的 media_id。
func (store *PostgresStore) ReviewMatchCandidateByID(ctx context.Context, candidateID int64, actorUserID int, decision, reason string) error {
	return store.ResolveMatchCandidateByID(ctx, candidateID, 0, actorUserID, decision, reason)
}

// ResolveMatchCandidateByID 按候选 ID 复核，并允许人工改判到另一个 media_id。
func (store *PostgresStore) ResolveMatchCandidateByID(ctx context.Context, candidateID int64, resolvedMediaID, actorUserID int, decision, reason string) error {
	if candidateID <= 0 {
		return errors.New("invalid resource match review")
	}
	return store.reviewMatchCandidate(ctx, candidateID, "", "", 0, resolvedMediaID, actorUserID, decision, strings.TrimSpace(reason))
}

// reviewMatchCandidate 在一个事务里完成复核：锁定候选行 → 写/改资源关联 →
// 同步剧集候选的 media_id → 更新候选状态。提交后再记一行日志留痕。
// 通过的关联会被标记 is_locked，之后自动匹配不能再覆盖它。
func (store *PostgresStore) reviewMatchCandidate(ctx context.Context, candidateID int64, sourceKey, vodID string, mediaID, resolvedMediaID, actorUserID int, decision, reason string) error {
	decision = normalizeMatchDecision(decision, "")
	if actorUserID <= 0 || reason == "" || (decision != MatchStatusVerified && decision != MatchStatusRejected) {
		return errors.New("invalid resource match review")
	}
	if len([]rune(reason)) > 500 {
		return errors.New("resource match review reason is too long")
	}
	if store.beginner == nil {
		return errors.New("resource match review requires transaction support")
	}
	transaction, err := store.beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin resource match review: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))

	var previousStatus, matchMethod string
	var confidence float64
	if candidateID > 0 {
		err = transaction.QueryRow(ctx, `SELECT id, source_key, vod_id, media_id, status, confidence, match_method
FROM resource_match_candidates
WHERE id = $1
FOR UPDATE`, candidateID).Scan(&candidateID, &sourceKey, &vodID, &mediaID, &previousStatus, &confidence, &matchMethod)
	} else {
		err = transaction.QueryRow(ctx, `SELECT id, source_key, vod_id, media_id, status, confidence, match_method
FROM resource_match_candidates
WHERE source_key = $1 AND vod_id = $2 AND media_id = $3
FOR UPDATE`, sourceKey, vodID, mediaID).Scan(&candidateID, &sourceKey, &vodID, &mediaID, &previousStatus, &confidence, &matchMethod)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("resource match candidate not found")
	}
	if err != nil {
		return fmt.Errorf("lock resource match candidate: %w", err)
	}
	if previousStatus != MatchStatusReview {
		return fmt.Errorf("resource match candidate is already %s", previousStatus)
	}
	if resolvedMediaID <= 0 {
		resolvedMediaID = mediaID
	}
	if decision == MatchStatusRejected {
		resolvedMediaID = 0
	}
	if decision == MatchStatusVerified {
		affected, err := transaction.Exec(ctx, `INSERT INTO resource_media_links
(source_key, vod_id, media_id, confidence, matched_by, is_locked, verified_at)
VALUES ($1,$2,$3,1.0000,'manual',TRUE,NOW())
ON CONFLICT (source_key, vod_id) DO UPDATE SET
media_id = EXCLUDED.media_id, confidence = EXCLUDED.confidence,
matched_by = EXCLUDED.matched_by, is_locked = TRUE, verified_at = NOW(), updated_at = NOW()
WHERE resource_media_links.is_locked = FALSE OR resource_media_links.media_id = EXCLUDED.media_id`,
			sourceKey, vodID, resolvedMediaID)
		if err != nil {
			return fmt.Errorf("confirm resource media link: %w", err)
		}
		if affected == 0 {
			return errors.New("resource is already locked to another media")
		}
		if _, err := transaction.Exec(ctx, `UPDATE resource_episode_candidates candidate
SET media_id = $3, updated_at = NOW()
FROM resource_play_lines line
WHERE candidate.line_id = line.id AND line.source_key = $1 AND line.vod_id = $2`, sourceKey, vodID, resolvedMediaID); err != nil {
			return fmt.Errorf("bind structured resource candidates: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `UPDATE resource_match_candidates
SET status = $2, resolved_media_id = $3, updated_at = NOW()
WHERE id = $1`, candidateID, decision, nullableMediaID(resolvedMediaID)); err != nil {
		return fmt.Errorf("update resource match candidate: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit resource match review: %w", err)
	}
	// 复核留痕写日志而不是写 resource_match_audits：那张表从建出来到删掉都没有读取方，
	// 想查还得自己开库敲 SQL。日志是现成的检索入口，字段一个不少。
	requestmeta.Logger(ctx).Info("resource match reviewed",
		"candidate_id", candidateID, "source", sourceKey, "vod_id", vodID,
		"media_id", mediaID, "resolved_media_id", resolvedMediaID,
		"actor_user_id", actorUserID, "decision", decision,
		"previous_status", previousStatus, "confidence", confidence,
		"match_method", matchMethod, "reason", reason)
	return nil
}

// normalizeMatchDecision 归一复核状态取值，非法值回退到 fallback。
func normalizeMatchDecision(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case MatchStatusReview, MatchStatusVerified, MatchStatusRejected:
		return value
	default:
		return fallback
	}
}
