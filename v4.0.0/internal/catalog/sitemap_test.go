package catalog

import (
	"testing"
	"time"
)

func TestSitemapProviderExposesLatestMovieURLsWithoutInventingPaths(t *testing.T) {
	store := NewMemoryStore()
	times := []time.Time{
		time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
	}
	index := 0
	store.now = func() time.Time { value := times[index]; index++; return value }
	_ = store.Upsert(t.Context(), Movie{DoubanID: "first", Title: "First"})
	_ = store.Upsert(t.Context(), Movie{DoubanID: "second", Title: "Second"})
	movies, err := NewSitemapProvider(store).LatestForSitemap(t.Context(), 1)
	if err != nil || len(movies) != 1 || movies[0].DoubanID != "second" || !movies[0].UpdatedAt.Equal(times[1]) {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
}
