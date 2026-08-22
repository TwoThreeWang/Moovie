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

// IndexResourceEpisodes 解析并写入某条资源的剧集候选。
func (adapter SearchAdapter) IndexResourceEpisodes(ctx context.Context, item search.VodItem) error {
	// 索引发生在 enrichMediaIdentity 之前，此时 item.MediaID 通常还是 0；
	// 而第 0 层命中已有关联时不会再调 LinkResource 触发回填。
	// 两者叠加会让剧集候选永远停留在 media_id = NULL，
	// 于是 /watch 按 media_id 查不到任何候选，只能 302 回搜索页。
	// 因此这里在写入前主动补一次已确认的关联。
	mediaID := item.MediaID
	if mediaID <= 0 {
		if link, err := adapter.Store.FindResourceLink(ctx, item.SourceKey, item.VodId); err == nil && link.MediaID > 0 {
			mediaID = link.MediaID
		}
	}
	episodes := ParseResourceEpisodes(item.SourceKey, item.VodId, mediaID, item.TypeName, item.VodPlayUrl)
	return adapter.Store.UpsertEpisodes(ctx, episodes)
}
