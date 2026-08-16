package catalog

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu     sync.RWMutex
	nextID int
	movies []Movie
	now    func() time.Time
}

func (store *MemoryStore) Suggest(_ context.Context, keyword string, limit int) ([]Movie, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	needle := strings.ToLower(keyword)
	movies := make([]Movie, 0)
	for _, movie := range store.movies {
		if strings.Contains(strings.ToLower(movie.Title), needle) || strings.Contains(strings.ToLower(movie.OriginalTitle), needle) {
			movies = append(movies, movie)
		}
	}
	sort.SliceStable(movies, func(i, j int) bool {
		leftRank, rightRank := memorySuggestRank(movies[i], keyword), memorySuggestRank(movies[j], keyword)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if movies[i].Year != movies[j].Year {
			if movies[i].Year == "" {
				return false
			}
			if movies[j].Year == "" {
				return true
			}
			return movies[i].Year < movies[j].Year
		}
		if movies[i].Rating == movies[j].Rating {
			return movies[i].UpdatedAt.After(movies[j].UpdatedAt)
		}
		return movies[i].Rating > movies[j].Rating
	})
	if limit >= 0 && limit < len(movies) {
		movies = movies[:limit]
	}
	return movies, nil
}

func memorySuggestRank(movie Movie, keyword string) int {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	title, original := strings.ToLower(strings.TrimSpace(movie.Title)), strings.ToLower(strings.TrimSpace(movie.OriginalTitle))
	if title == keyword || original == keyword {
		return 0
	}
	if strings.HasPrefix(title, keyword) || strings.HasPrefix(original, keyword) {
		return 1
	}
	return 2
}

func (store *MemoryStore) Popular(_ context.Context, limit int) ([]Movie, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	movies := make([]Movie, 0)
	for _, movie := range store.movies {
		if movie.Rating > 0 {
			movies = append(movies, movie)
		}
	}
	sort.SliceStable(movies, func(i, j int) bool {
		if movies[i].Rating == movies[j].Rating {
			return movies[i].UpdatedAt.After(movies[j].UpdatedAt)
		}
		return movies[i].Rating > movies[j].Rating
	})
	if limit >= 0 && limit < len(movies) {
		movies = movies[:limit]
	}
	return movies, nil
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, now: time.Now}
}

func (store *MemoryStore) Count(_ context.Context) (int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return len(store.movies), nil
}

func (store *MemoryStore) FindByDoubanID(_ context.Context, doubanID string) (*Movie, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, movie := range store.movies {
		if movie.DoubanID == doubanID {
			copy := movie
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) FindByID(_ context.Context, id int) (*Movie, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, movie := range store.movies {
		if movie.ID == id {
			copy := movie
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) FindSimilar(_ context.Context, doubanID string, limit int) ([]Movie, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	var source *Movie
	for index := range store.movies {
		if store.movies[index].DoubanID == doubanID {
			source = &store.movies[index]
			break
		}
	}
	if source == nil {
		return []Movie{}, nil
	}
	type scored struct {
		movie Movie
		score int
	}
	scoredMovies := make([]scored, 0)
	sourceGenres := stringSet(source.Genres)
	for _, movie := range store.movies {
		if movie.DoubanID == doubanID {
			continue
		}
		score := 0
		for genre := range stringSet(movie.Genres) {
			if sourceGenres[genre] {
				score += 10
			}
		}
		if movie.Directors != "" && movie.Directors == source.Directors {
			score += 7
		}
		if movie.Actors != "" && movie.Actors == source.Actors {
			score += 4
		}
		scoredMovies = append(scoredMovies, scored{movie: movie, score: score})
	}
	sort.SliceStable(scoredMovies, func(i, j int) bool {
		if scoredMovies[i].score == scoredMovies[j].score {
			return scoredMovies[i].movie.Rating > scoredMovies[j].movie.Rating
		}
		return scoredMovies[i].score > scoredMovies[j].score
	})
	movies := make([]Movie, 0, len(scoredMovies))
	for _, candidate := range scoredMovies {
		movies = append(movies, candidate.movie)
	}
	if limit >= 0 && limit < len(movies) {
		movies = movies[:limit]
	}
	return movies, nil
}

func stringSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result[part] = true
		}
	}
	return result
}

func (store *MemoryStore) Upsert(_ context.Context, movie Movie) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	movie.UpdatedAt = store.now()
	for index := range store.movies {
		if store.movies[index].DoubanID == movie.DoubanID {
			movie.ID = store.movies[index].ID
			movie.EmbeddingContent = store.movies[index].EmbeddingContent
			movie.EmbeddingSemanticHash = store.movies[index].EmbeddingSemanticHash
			movie.Embedding = append([]float32(nil), store.movies[index].Embedding...)
			store.movies[index] = movie
			return nil
		}
	}
	movie.ID = store.nextID
	store.nextID++
	store.movies = append(store.movies, movie)
	return nil
}

func (store *MemoryStore) UpdateEmbedding(_ context.Context, doubanID, content, semanticHash string, embedding []float32) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.movies {
		if store.movies[index].DoubanID == doubanID {
			store.movies[index].EmbeddingContent = content
			store.movies[index].EmbeddingSemanticHash = semanticHash
			store.movies[index].Embedding = append([]float32(nil), embedding...)
			store.movies[index].UpdatedAt = store.now()
			return nil
		}
	}
	return nil
}

func (store *MemoryStore) DeleteByDoubanID(_ context.Context, doubanID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.movies {
		if store.movies[index].DoubanID == doubanID {
			store.movies = append(store.movies[:index], store.movies[index+1:]...)
			break
		}
	}
	return nil
}

func (store *MemoryStore) Latest(_ context.Context, limit int) ([]Movie, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	movies := append([]Movie(nil), store.movies...)
	sort.SliceStable(movies, func(i, j int) bool { return movies[i].UpdatedAt.After(movies[j].UpdatedAt) })
	if limit >= 0 && limit < len(movies) {
		movies = movies[:limit]
	}
	return movies, nil
}
