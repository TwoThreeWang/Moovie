package mediaidentity

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/search"
)

// MatchResource 把 search 包的请求结构翻译成本包的结构，再调打分匹配。
func (adapter SearchAdapter) MatchResource(ctx context.Context, request search.MediaMatchRequest) (search.MediaMatchResult, error) {
	result, err := adapter.Store.MatchResource(ctx, MatchInput{Title: request.Title, OriginalTitle: request.OriginalTitle,
		Year: request.Year, MediaType: request.MediaType, Actors: request.Actors, Directors: request.Directors})
	if err != nil {
		return search.MediaMatchResult{}, err
	}
	return search.MediaMatchResult{MediaID: result.MediaID, Confidence: result.Confidence, MatchedBy: result.MatchedBy,
		Status: result.Status, ReasonJSON: result.ReasonJSON, HardConflict: result.HardConflict}, nil
}

// RecordDetailedMatchCandidate 记录一条待复核的匹配候选。
func (adapter SearchAdapter) RecordDetailedMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error {
	return adapter.Store.RecordDetailedMatchCandidate(ctx, sourceKey, vodID, mediaID, confidence, matchedBy, status, reasonJSON)
}
