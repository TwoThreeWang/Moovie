package playback

import (
	"context"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
)

type Catalog interface {
	Search(ctx context.Context, keyword string) ([]search.VodItem, error)
	FindBySourceID(ctx context.Context, sourceKey, vodID string) (*search.VodItem, error)
	SearchByDoubanID(ctx context.Context, doubanID string) ([]search.VodItem, error)
	Upsert(ctx context.Context, item search.VodItem) error
}

type SiteCatalog interface {
	FindSiteByKey(ctx context.Context, key string) (*search.Site, error)
}

type DetailCrawler interface {
	GetDetail(ctx context.Context, baseURL, vodID, sourceKey string) (*search.VodItem, error)
}

type BackgroundRunner interface {
	Run(task func(context.Context))
}

type MovieTitleFinder interface {
	FindTitleByDoubanID(ctx context.Context, doubanID string) (string, error)
}

type PopularProvider interface {
	Popular(ctx context.Context, mediaType string) ([]PopularSubject, error)
}

type PopularIdentityResolver interface {
	FindByExternalID(ctx context.Context, provider, externalType, externalID string) (mediaidentity.Media, error)
}

type SpeedStore interface {
	LoadStats(ctx context.Context, sourceKey, vodID string) (*search.LoadStats, error)
}

type CopyrightChecker interface {
	IsCopyrightRestricted(ctx context.Context, title string) (bool, string)
}

type UserMovieStore interface {
	IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error)
}
