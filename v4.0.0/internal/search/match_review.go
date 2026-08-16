package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	MatchStatusReview   = "review"
	MatchStatusVerified = "verified"
	MatchStatusRejected = "rejected"
)

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

type MatchReviewStore interface {
	ListMatchCandidates(ctx context.Context, status string, limit int) ([]MatchCandidate, error)
	ReviewMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID, actorUserID int, decision, reason string) error
	ReviewMatchCandidateByID(ctx context.Context, candidateID int64, actorUserID int, decision, reason string) error
	ResolveMatchCandidateByID(ctx context.Context, candidateID int64, resolvedMediaID, actorUserID int, decision, reason string) error
}

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

func (store *PostgresStore) ReviewMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID, actorUserID int, decision, reason string) error {
	sourceKey, vodID, reason = strings.TrimSpace(sourceKey), strings.TrimSpace(vodID), strings.TrimSpace(reason)
	if sourceKey == "" || vodID == "" || mediaID <= 0 {
		return errors.New("invalid resource match review")
	}
	return store.reviewMatchCandidate(ctx, 0, sourceKey, vodID, mediaID, 0, actorUserID, decision, reason)
}

func (store *PostgresStore) ReviewMatchCandidateByID(ctx context.Context, candidateID int64, actorUserID int, decision, reason string) error {
	return store.ResolveMatchCandidateByID(ctx, candidateID, 0, actorUserID, decision, reason)
}

func (store *PostgresStore) ResolveMatchCandidateByID(ctx context.Context, candidateID int64, resolvedMediaID, actorUserID int, decision, reason string) error {
	if candidateID <= 0 {
		return errors.New("invalid resource match review")
	}
	return store.reviewMatchCandidate(ctx, candidateID, "", "", 0, resolvedMediaID, actorUserID, decision, strings.TrimSpace(reason))
}

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
	if _, err := transaction.Exec(ctx, `INSERT INTO resource_match_audits
(candidate_id, source_key, vod_id, media_id, previous_media_id, resolved_media_id,
	 actor_user_id, decision, previous_status, confidence, match_method, reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, candidateID, sourceKey, vodID, mediaID, mediaID,
		nullableResolvedMediaID(resolvedMediaID, decision), actorUserID, decision, previousStatus, confidence, matchMethod, reason); err != nil {
		return fmt.Errorf("audit resource match decision: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit resource match review: %w", err)
	}
	return nil
}

func nullableResolvedMediaID(mediaID int, decision string) any {
	if decision != MatchStatusVerified {
		return nil
	}
	return mediaID
}

func normalizeMatchDecision(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case MatchStatusReview, MatchStatusVerified, MatchStatusRejected:
		return value
	default:
		return fallback
	}
}
