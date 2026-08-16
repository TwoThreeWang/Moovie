package recommendation

import (
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/history"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

func TestMemoryPersonalizerUsesWishWatchedAndHistoryWithoutRepeatingThem(t *testing.T) {
	catalogStore := catalog.NewMemoryStore()
	libraryStore := library.NewMemoryStore()
	historyStore := history.NewMemoryStore()
	for _, movie := range []catalog.Movie{
		{DoubanID: "wish", Title: "想看源", Genres: "科幻", Rating: 9}, {DoubanID: "watched", Title: "看过源", Genres: "剧情", Rating: 9},
		{DoubanID: "history", Title: "历史源", Genres: "悬疑", Rating: 8}, {DoubanID: "science", Title: "科幻候选", Genres: "科幻", Rating: 8.8},
		{DoubanID: "drama", Title: "剧情候选", Genres: "剧情", Rating: 8.7}, {DoubanID: "mystery", Title: "悬疑候选", Genres: "悬疑", Rating: 8.6},
	} {
		_ = catalogStore.Upsert(t.Context(), movie)
	}
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: 7, MovieID: "wish", Status: library.StatusWish, Title: "想看源"})
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: 7, MovieID: "watched", Status: library.StatusWatched, Title: "看过源"})
	_ = historyStore.Upsert(t.Context(), history.Record{UserID: 7, DoubanID: "history", VodID: "v", Source: "s", Title: "历史源", Progress: 10, WatchedAt: time.Now().Add(time.Hour)})
	personalizer := NewMemoryPersonalizer(catalogStore, libraryStore, historyStore)
	movies, err := personalizer.UserRecommendations(t.Context(), 7, 10)
	if err != nil || len(movies) < 3 {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
	seen := map[string]bool{}
	for _, movie := range movies {
		seen[movie.DoubanID] = true
	}
	for _, excluded := range []string{"wish", "watched", "history"} {
		if seen[excluded] {
			t.Fatalf("interaction %s leaked into recommendations: %+v", excluded, movies)
		}
	}
	for _, expected := range []string{"science", "drama", "mystery"} {
		if !seen[expected] {
			t.Fatalf("candidate %s missing: %+v", expected, movies)
		}
	}
	recent, title, err := personalizer.RecentSimilar(t.Context(), 7, 10)
	if err != nil || title != "历史源" || len(recent) == 0 {
		t.Fatalf("recent/title/error = %+v/%q/%v", recent, title, err)
	}
}

func TestMemoryPersonalizerReliveUsesThirtyDayAndCatalogRatingRules(t *testing.T) {
	catalogStore := catalog.NewMemoryStore()
	libraryStore := library.NewMemoryStore()
	historyStore := history.NewMemoryStore()
	_ = catalogStore.Upsert(t.Context(), catalog.Movie{DoubanID: "classic", Title: "经典", Rating: 9})
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: 7, MovieID: "classic", Status: library.StatusWatched})
	records, _ := libraryStore.ListByUser(t.Context(), 7, library.StatusWatched, 1, 0)
	personalizer := NewMemoryPersonalizer(catalogStore, libraryStore, historyStore)
	personalizer.now = func() time.Time { return records[0].UpdatedAt.Add(31 * 24 * time.Hour) }
	movies, err := personalizer.ReliveClassics(t.Context(), 7, 12)
	if err != nil || len(movies) != 1 || movies[0].DoubanID != "classic" {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
}
