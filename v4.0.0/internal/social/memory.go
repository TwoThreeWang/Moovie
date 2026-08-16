package social

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

type memoryUserStore interface {
	ListUsers(ctx context.Context) ([]identity.User, error)
}

type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int
	likes   map[int]map[int]bool
	replies []Reply
	library library.Store
	users   memoryUserStore
	now     func() time.Time
}

func NewMemoryStore(libraryStore library.Store, users memoryUserStore) *MemoryStore {
	return &MemoryStore{nextID: 1, likes: make(map[int]map[int]bool), library: libraryStore, users: users, now: time.Now}
}

func (store *MemoryStore) ListCommentsByMovie(ctx context.Context, movieID string, limit int) ([]Activity, error) {
	activities, err := store.allActivities(ctx, false)
	if err != nil {
		return nil, err
	}
	filtered := make([]Activity, 0)
	for _, activity := range activities {
		if activity.MovieID == movieID && activity.Status == library.StatusWatched && activity.Comment != "" {
			filtered = append(filtered, activity)
		}
	}
	return takeActivities(filtered, limit, 0), nil
}

func (store *MemoryStore) CountLikes(_ context.Context, ids []int) (map[int]int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	counts := make(map[int]int, len(ids))
	for _, id := range ids {
		counts[id] = len(store.likes[id])
	}
	return counts, nil
}

func (store *MemoryStore) CountReplies(_ context.Context, ids []int) (map[int]int, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	requested := make(map[int]bool, len(ids))
	counts := make(map[int]int, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	for _, reply := range store.replies {
		if requested[reply.UserMovieID] {
			counts[reply.UserMovieID]++
		}
	}
	return counts, nil
}

func (store *MemoryStore) LikedByUser(_ context.Context, ids []int, userID int) (map[int]bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	liked := make(map[int]bool, len(ids))
	if userID <= 0 {
		return liked, nil
	}
	for _, id := range ids {
		liked[id] = store.likes[id][userID]
	}
	return liked, nil
}

func (store *MemoryStore) ToggleLike(ctx context.Context, userMovieID, userID int) (int, bool, error) {
	if !store.recordExists(ctx, userMovieID) {
		return 0, false, fmt.Errorf("user movie not found")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.likes[userMovieID] == nil {
		store.likes[userMovieID] = make(map[int]bool)
	}
	liked := !store.likes[userMovieID][userID]
	if liked {
		store.likes[userMovieID][userID] = true
	} else {
		delete(store.likes[userMovieID], userID)
	}
	return len(store.likes[userMovieID]), liked, nil
}

func (store *MemoryStore) ListReplies(ctx context.Context, userMovieID int) ([]Reply, error) {
	users, err := store.userMap(ctx)
	if err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	replies := make([]Reply, 0)
	for _, reply := range store.replies {
		if reply.UserMovieID == userMovieID {
			reply.User = users[reply.UserID]
			replies = append(replies, reply)
		}
	}
	sort.SliceStable(replies, func(i, j int) bool { return replies[i].CreatedAt.Before(replies[j].CreatedAt) })
	return replies, nil
}

func (store *MemoryStore) CreateReply(ctx context.Context, userMovieID, userID int, content string) (*Reply, error) {
	if !store.recordExists(ctx, userMovieID) {
		return nil, fmt.Errorf("user movie not found")
	}
	users, err := store.userMap(ctx)
	if err != nil {
		return nil, err
	}
	user, exists := users[userID]
	if !exists {
		return nil, fmt.Errorf("user not found")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	reply := Reply{ID: store.nextID, UserMovieID: userMovieID, UserID: userID, Content: content, CreatedAt: store.now(), User: user}
	store.nextID++
	store.replies = append(store.replies, reply)
	return &reply, nil
}

func (store *MemoryStore) ListWeeklyFilms(ctx context.Context, since time.Time, limit int) ([]WeeklyFilm, error) {
	activities, err := store.allActivities(ctx, true)
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		film        WeeklyFilm
		viewers     map[int]bool
		ratingTotal int
		ratingCount int
	}
	aggregates := make(map[string]*aggregate)
	for _, activity := range activities {
		if activity.Status != library.StatusWatched || activity.MovieID == "" || activity.CreatedAt.Before(since) {
			continue
		}
		entry := aggregates[activity.MovieID]
		if entry == nil {
			entry = &aggregate{film: WeeklyFilm{
				MovieID: activity.MovieID, Title: activity.Title, Poster: activity.Poster,
				Year: activity.Year, LastSeenAt: activity.CreatedAt,
			}, viewers: make(map[int]bool)}
			aggregates[activity.MovieID] = entry
		}
		entry.viewers[activity.UserID] = true
		if strings.TrimSpace(activity.Comment) != "" {
			entry.film.CommentCount++
		}
		if activity.Rating > 0 {
			entry.ratingTotal += activity.Rating
			entry.ratingCount++
		}
		if activity.CreatedAt.After(entry.film.LastSeenAt) {
			entry.film.LastSeenAt = activity.CreatedAt
		}
	}
	films := make([]WeeklyFilm, 0, len(aggregates))
	for _, entry := range aggregates {
		entry.film.ViewerCount = len(entry.viewers)
		if entry.ratingCount > 0 {
			entry.film.AverageRating = float64(entry.ratingTotal) / float64(entry.ratingCount)
		}
		films = append(films, entry.film)
	}
	sort.SliceStable(films, func(i, j int) bool {
		if films[i].LastSeenAt.Equal(films[j].LastSeenAt) {
			return films[i].ViewerCount > films[j].ViewerCount
		}
		return films[i].LastSeenAt.After(films[j].LastSeenAt)
	})
	if limit >= 0 && len(films) > limit {
		films = films[:limit]
	}
	return films, nil
}

func (store *MemoryStore) ListFeaturedComments(ctx context.Context, limit int) ([]Activity, error) {
	if limit <= 0 {
		return []Activity{}, nil
	}
	activities, err := store.allActivities(ctx, true)
	if err != nil {
		return nil, err
	}
	comments := make([]Activity, 0, limit)
	perUser := make(map[int]int)
	for _, activity := range activities {
		if activity.Status != library.StatusWatched || strings.TrimSpace(activity.Comment) == "" || perUser[activity.UserID] >= 2 {
			continue
		}
		comments = append(comments, activity)
		perUser[activity.UserID]++
		if len(comments) == limit {
			break
		}
	}
	return comments, nil
}

func (store *MemoryStore) ListFilmFriends(ctx context.Context, currentUserID, limit int) ([]FilmFriend, error) {
	activities, err := store.allActivities(ctx, true)
	if err != nil {
		return nil, err
	}
	mine := make(map[string]bool)
	if currentUserID > 0 {
		records, listErr := store.library.ListByUser(ctx, currentUserID, library.StatusWatched, int(^uint(0)>>1), 0)
		if listErr != nil {
			return nil, listErr
		}
		for _, record := range records {
			mine[record.MovieID] = true
		}
	}
	indexed := make(map[int]*FilmFriend)
	for _, activity := range activities {
		if activity.UserID == currentUserID || activity.Status != library.StatusWatched {
			continue
		}
		friend := indexed[activity.UserID]
		if friend == nil {
			friend = &FilmFriend{UserID: activity.UserID, Username: activity.User.Username, Avatar: activity.User.Avatar}
			indexed[activity.UserID] = friend
		}
		friend.WatchedCount++
		if strings.TrimSpace(activity.Comment) != "" {
			friend.CommentCount++
		}
		if mine[activity.MovieID] {
			friend.SharedCount++
		}
		if activity.CreatedAt.After(friend.LastActiveAt) {
			friend.LastActiveAt = activity.CreatedAt
		}
	}
	friends := make([]FilmFriend, 0, len(indexed))
	for _, friend := range indexed {
		friends = append(friends, *friend)
	}
	sort.SliceStable(friends, func(i, j int) bool {
		if friends[i].SharedCount != friends[j].SharedCount {
			return friends[i].SharedCount > friends[j].SharedCount
		}
		if friends[i].CommentCount != friends[j].CommentCount {
			return friends[i].CommentCount > friends[j].CommentCount
		}
		return friends[i].LastActiveAt.After(friends[j].LastActiveAt)
	})
	if limit >= 0 && len(friends) > limit {
		friends = friends[:limit]
	}
	return friends, nil
}

func (store *MemoryStore) allActivities(ctx context.Context, publicOnly bool) ([]Activity, error) {
	users, err := store.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	activities := make([]Activity, 0)
	for _, user := range users {
		if publicOnly && !user.IsPublic {
			continue
		}
		records, listErr := store.library.ListByUser(ctx, user.ID, library.StatusWatched, int(^uint(0)>>1), 0)
		if listErr != nil {
			return nil, listErr
		}
		for _, record := range records {
			activities = append(activities, Activity{Record: record, User: user})
		}
	}
	sort.SliceStable(activities, func(i, j int) bool {
		if activities[i].UpdatedAt.Equal(activities[j].UpdatedAt) {
			return activities[i].ID > activities[j].ID
		}
		return activities[i].UpdatedAt.After(activities[j].UpdatedAt)
	})
	return activities, nil
}

func (store *MemoryStore) userMap(ctx context.Context) (map[int]identity.User, error) {
	users, err := store.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	indexed := make(map[int]identity.User, len(users))
	for _, user := range users {
		indexed[user.ID] = user
	}
	return indexed, nil
}

func (store *MemoryStore) recordExists(ctx context.Context, id int) bool {
	users, err := store.users.ListUsers(ctx)
	if err != nil {
		return false
	}
	for _, user := range users {
		record, _ := store.library.GetByID(ctx, user.ID, id)
		if record != nil {
			return true
		}
	}
	return false
}

func takeActivities(activities []Activity, limit, offset int) []Activity {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(activities) {
		return []Activity{}
	}
	end := len(activities)
	if limit >= 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]Activity(nil), activities[offset:end]...)
}
