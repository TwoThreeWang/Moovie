package mediaidentity

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/search"
)

func (adapter SearchAdapter) MatchResource(ctx context.Context, request search.MediaMatchRequest) (search.MediaMatchResult, error) {
	result, err := adapter.Store.MatchResource(ctx, MatchInput{Title: request.Title, OriginalTitle: request.OriginalTitle,
		Year: request.Year, MediaType: request.MediaType, Actors: request.Actors, Directors: request.Directors})
	if err != nil {
		return search.MediaMatchResult{}, err
	}
	return search.MediaMatchResult{MediaID: result.MediaID, Confidence: result.Confidence, MatchedBy: result.MatchedBy,
		Status: result.Status, ReasonJSON: result.ReasonJSON, HardConflict: result.HardConflict}, nil
}

func (adapter SearchAdapter) RecordDetailedMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error {
	return adapter.Store.RecordDetailedMatchCandidate(ctx, sourceKey, vodID, mediaID, confidence, matchedBy, status, reasonJSON)
}

func (adapter SearchAdapter) IndexResourceEpisodes(ctx context.Context, item search.VodItem) error {
	episodes := ParseResourceEpisodes(item.SourceKey, item.VodId, item.MediaID, item.TypeName, item.VodPlayUrl)
	return adapter.Store.UpsertEpisodes(ctx, episodes)
}
