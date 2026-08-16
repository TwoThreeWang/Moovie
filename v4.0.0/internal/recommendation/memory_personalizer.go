package recommendation

import (
	"context"
	"sort"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/history"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

type LibraryStore interface {
	ListByUser(context.Context, int, string, int, int) ([]library.Record, error)
}
type HistoryStore interface {
	ListByUser(context.Context, int, int, int) ([]history.Record, error)
}
type CatalogStore interface {
	FindByDoubanID(context.Context, string) (*catalog.Movie, error)
	FindSimilar(context.Context, string, int) ([]catalog.Movie, error)
}

type MemoryPersonalizer struct {
	catalog CatalogStore
	library LibraryStore
	history HistoryStore
	now     func() time.Time
}

func NewMemoryPersonalizer(catalogStore CatalogStore, libraryStore LibraryStore, historyStore HistoryStore) *MemoryPersonalizer {
	return &MemoryPersonalizer{catalog: catalogStore, library: libraryStore, history: historyStore, now: time.Now}
}

func (personalizer *MemoryPersonalizer) UserRecommendations(ctx context.Context, userID, limit int) ([]catalog.Movie, error) {
	type seed struct {
		id     string
		weight float64
	}
	seeds := make([]seed, 0)
	excluded := make(map[string]bool)
	seeded := make(map[string]bool)
	watched, _ := personalizer.library.ListByUser(ctx, userID, library.StatusWatched, 10000, 0)
	wish, _ := personalizer.library.ListByUser(ctx, userID, library.StatusWish, 10000, 0)
	histories, _ := personalizer.history.ListByUser(ctx, userID, 10000, 0)
	for _, record := range watched {
		seeds = append(seeds, seed{record.MovieID, 1})
		excluded[record.MovieID] = true
		seeded[record.MovieID] = true
	}
	for _, record := range wish {
		seeds = append(seeds, seed{record.MovieID, 2})
		excluded[record.MovieID] = true
		seeded[record.MovieID] = true
	}
	for _, record := range histories {
		if record.Progress > 5 && record.DoubanID != "" {
			excluded[record.DoubanID] = true
			if !seeded[record.DoubanID] {
				seeds = append(seeds, seed{record.DoubanID, 0.8})
				seeded[record.DoubanID] = true
			}
		}
	}
	type scored struct {
		movie catalog.Movie
		score float64
	}
	byID := make(map[string]scored)
	for _, source := range seeds {
		movies, _ := personalizer.catalog.FindSimilar(ctx, source.id, 60)
		for rank, movie := range movies {
			if excluded[movie.DoubanID] {
				continue
			}
			score := source.weight / float64(rank+1)
			current := byID[movie.DoubanID]
			current.movie = movie
			current.score += score
			byID[movie.DoubanID] = current
		}
	}
	items := make([]scored, 0, len(byID))
	for _, item := range byID {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].movie.Rating > items[j].movie.Rating
		}
		return items[i].score > items[j].score
	})
	result := make([]catalog.Movie, 0, len(items))
	for _, item := range items {
		result = append(result, item.movie)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (personalizer *MemoryPersonalizer) ReliveClassics(ctx context.Context, userID, limit int) ([]catalog.Movie, error) {
	records, _ := personalizer.library.ListByUser(ctx, userID, library.StatusWatched, 10000, 0)
	result := make([]catalog.Movie, 0)
	cutoff := personalizer.now().Add(-30 * 24 * time.Hour)
	for _, record := range records {
		if !record.UpdatedAt.Before(cutoff) {
			continue
		}
		movie, _ := personalizer.catalog.FindByDoubanID(ctx, record.MovieID)
		if movie != nil && movie.Rating >= 5 {
			result = append(result, *movie)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (personalizer *MemoryPersonalizer) RecentSimilar(ctx context.Context, userID, limit int) ([]catalog.Movie, string, error) {
	records, _ := personalizer.library.ListByUser(ctx, userID, library.StatusWatched, 1, 0)
	histories, _ := personalizer.history.ListByUser(ctx, userID, 10000, 0)
	id, title := "", ""
	latest := time.Time{}
	if len(records) > 0 {
		id, title, latest = records[0].MovieID, records[0].Title, records[0].UpdatedAt
	}
	for _, record := range histories {
		if record.Progress > 5 && record.DoubanID != "" && record.WatchedAt.After(latest) {
			id, title, latest = record.DoubanID, record.Title, record.WatchedAt
			break
		}
	}
	if id == "" {
		return []catalog.Movie{}, "", nil
	}
	if title == "" {
		if movie, _ := personalizer.catalog.FindByDoubanID(ctx, id); movie != nil {
			title = movie.Title
		}
	}
	movies, err := personalizer.catalog.FindSimilar(ctx, id, limit)
	return movies, title, err
}
