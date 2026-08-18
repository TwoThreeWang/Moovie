package catalog

import (
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestSitemapProviderExposesLatestMovieURLsWithoutInventingPaths(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "first", Title: "First"})
	_ = store.Upsert(t.Context(), Movie{DoubanID: "second", Title: "Second"})
	movies, err := NewSitemapProvider(store).LatestForSitemap(t.Context(), 1)
	if err != nil || len(movies) != 1 || movies[0].DoubanID != "second" || movies[0].UpdatedAt.IsZero() {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
}
