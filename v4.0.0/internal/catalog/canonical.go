package catalog

import (
	"context"
	"encoding/json"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
)

// CanonicalWriter 把抓到的资料写进规范媒体表，并留一份原始报文快照便于回溯。
type CanonicalWriter interface {
	Upsert(ctx context.Context, media mediaidentity.Media) (mediaidentity.Media, error)
	UpsertExternalID(ctx context.Context, external mediaidentity.ExternalID) error
	WriteSourceSnapshot(ctx context.Context, mediaID int, provider string, payload []byte, success bool, errorMessage string) error
}

// CanonicalSourceWriter 按字段级来源优先级应用 Provider 刷新。
// PostgreSQL 实现使用它；轻量内存实现只需满足 CanonicalWriter。
type CanonicalSourceWriter interface {
	MergeSource(ctx context.Context, provider string, media mediaidentity.Media, payload []byte, externalIDs ...mediaidentity.ExternalID) (mediaidentity.Media, error)
}

// syncCanonical 把 Movie 转成规范媒体结构再落库。
func syncCanonical(ctx context.Context, writer CanonicalWriter, movie Movie, mediaType, provider string, payload any, externalIDs ...mediaidentity.ExternalID) (int, error) {
	return syncCanonicalMedia(ctx, writer, mediaidentity.Media{
		MediaType: mediaType, DoubanID: movie.DoubanID, Title: movie.Title,
		OriginalTitle: movie.OriginalTitle, Year: movie.Year, Poster: movie.Poster,
		Backdrops: movie.Backdrops, Summary: movie.Summary, Genres: movie.Genres,
		Countries: movie.Countries, Directors: movie.Directors, Actors: movie.Actors,
		Duration: movie.Duration, RatingDouban: movie.Rating, MetadataStatus: "partial",
	}, provider, payload, externalIDs...)
}

// syncCanonicalMedia 落库规范媒体：能按来源优先级合并就合并，否则退化成简单覆盖写。
// writer 为 nil 表示没开规范化，直接返回 0。
func syncCanonicalMedia(ctx context.Context, writer CanonicalWriter, media mediaidentity.Media, provider string, payload any, externalIDs ...mediaidentity.ExternalID) (int, error) {
	if writer == nil {
		return 0, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	if merger, ok := writer.(CanonicalSourceWriter); ok {
		merged, err := merger.MergeSource(ctx, provider, media, encoded, externalIDs...)
		if err != nil {
			return 0, err
		}
		return merged.ID, nil
	}
	canonical, err := writer.Upsert(ctx, media)
	if err != nil {
		return 0, err
	}
	for _, external := range externalIDs {
		if external.ExternalID == "" {
			continue
		}
		external.MediaID = canonical.ID
		if external.ExternalType == "" {
			external.ExternalType = media.MediaType
		}
		if err := writer.UpsertExternalID(ctx, external); err != nil {
			return canonical.ID, err
		}
	}
	return canonical.ID, writer.WriteSourceSnapshot(ctx, canonical.ID, provider, encoded, true, "")
}
