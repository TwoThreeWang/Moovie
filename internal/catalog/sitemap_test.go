package catalog

import (
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/content"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestSitemapProviderExposesStableMoviePages(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "first", Title: "First"})
	_ = store.Upsert(t.Context(), Movie{DoubanID: "second", Title: "Second"})
	provider := NewSitemapProvider(store)
	count, err := provider.CountForSitemap(t.Context(), content.SitemapMovies)
	if err != nil || count < 2 {
		t.Fatalf("count/error = %d/%v", count, err)
	}
	movies, err := provider.PageForSitemap(t.Context(), content.SitemapMovies, 1, 0)
	if err != nil || len(movies) != 1 || movies[0].DoubanID == "" || movies[0].UpdatedAt.IsZero() {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
}
