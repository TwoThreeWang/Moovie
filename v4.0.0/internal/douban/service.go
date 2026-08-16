package douban

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

type InterestProvider interface {
	ValidateUser(ctx context.Context, doubanUserID string) error
	Interests(ctx context.Context, doubanUserID, itemType, status string, start, count int) ([]Interest, int, error)
	RSSSubjects(ctx context.Context, doubanUserID string) (map[string]bool, time.Time, error)
}

type Service struct {
	provider InterestProvider
	library  library.Store
	jobs     JobStore
	pageSize int
	delay    func(context.Context) error
}

type ServiceOption func(*Service)

func WithPageDelay(delay func(context.Context) error) ServiceOption {
	return func(service *Service) { service.delay = delay }
}

func NewService(provider InterestProvider, libraryStore library.Store, jobs JobStore, options ...ServiceOption) *Service {
	service := &Service{provider: provider, library: libraryStore, jobs: jobs, pageSize: 20, delay: defaultPageDelay}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *Service) ValidateUser(ctx context.Context, doubanUserID string) error {
	return service.provider.ValidateUser(ctx, doubanUserID)
}

func (service *Service) SyncFull(ctx context.Context, userID int, doubanUserID string, jobID int) error {
	processed, failed, totalAll := 0, 0, 0
	for _, itemType := range []string{"movie", "tv"} {
		for _, status := range []string{"mark", "done"} {
			start := 0
			for {
				items, total, err := service.provider.Interests(ctx, doubanUserID, itemType, status, start, service.pageSize)
				if err != nil {
					return err
				}
				if start == 0 {
					totalAll += total
					if err := service.jobs.UpdateTotal(ctx, jobID, totalAll); err != nil {
						return err
					}
				}
				service.persistPage(ctx, userID, items, &processed, &failed)
				if err := service.jobs.UpdateProgress(ctx, jobID, processed, failed, strconv.Itoa(start+len(items))); err != nil {
					return err
				}
				if len(items) == 0 || start+len(items) >= total {
					break
				}
				start += service.pageSize
				if err := service.delay(ctx); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (service *Service) SyncIncremental(ctx context.Context, userID int, doubanUserID string, jobID int) error {
	subjects, earliest, err := service.provider.RSSSubjects(ctx, doubanUserID)
	if err != nil {
		return err
	}
	if len(subjects) == 0 {
		return service.jobs.UpdateProgress(ctx, jobID, 0, 0, "")
	}
	processed, failed := 0, 0
	for _, itemType := range []string{"movie", "tv"} {
		for _, status := range []string{"mark", "done"} {
			start := 0
			for {
				items, _, err := service.provider.Interests(ctx, doubanUserID, itemType, status, start, service.pageSize)
				if err != nil {
					return err
				}
				outOfRange := false
				for _, item := range items {
					createdAt := parseDoubanTime(item.CreateTime)
					if !earliest.IsZero() && !createdAt.IsZero() && createdAt.Before(earliest) {
						outOfRange = true
						break
					}
					if subjects[item.Subject.ID.String()] {
						service.persistInterest(ctx, userID, item, &processed, &failed)
					}
				}
				if err := service.jobs.UpdateProgress(ctx, jobID, processed, failed, strconv.Itoa(start+len(items))); err != nil {
					return err
				}
				if outOfRange || len(items) == 0 {
					break
				}
				start += service.pageSize
				if err := service.delay(ctx); err != nil {
					return err
				}
			}
		}
	}
	return service.jobs.UpdateProgress(ctx, jobID, processed, failed, "")
}

func (service *Service) persistPage(ctx context.Context, userID int, items []Interest, processed, failed *int) {
	for _, item := range items {
		service.persistInterest(ctx, userID, item, processed, failed)
	}
}

func (service *Service) persistInterest(ctx context.Context, userID int, item Interest, processed, failed *int) {
	if !allowedSubject(item.Subject) {
		return
	}
	if err := service.upsertInterest(ctx, userID, item); err != nil {
		*failed++
		return
	}
	*processed++
}

func (service *Service) upsertInterest(ctx context.Context, userID int, item Interest) error {
	movieID := item.Subject.ID.String()
	if movieID == "" {
		return fmt.Errorf("豆瓣条目缺少 subject ID")
	}
	status := library.StatusWish
	if item.Status == "done" {
		status = library.StatusWatched
	}
	rating := 0
	if item.Rating != nil {
		rating = int(item.Rating.Value)
	}
	comment := item.Comment
	existing, err := service.library.GetByUserAndMovie(ctx, userID, movieID)
	if err != nil {
		return err
	}
	if existing != nil {
		if rating == 0 && existing.Rating > 0 {
			rating = existing.Rating
		}
		if comment == "" && existing.Comment != "" {
			comment = existing.Comment
		}
	}
	poster := item.Subject.CoverURL
	if poster == "" {
		poster = item.Subject.Pic.Large
	}
	createdAt := parseDoubanTime(item.CreateTime)
	return service.library.Upsert(ctx, library.Record{
		UserID: userID, MovieID: movieID, Title: item.Subject.Title, Poster: poster, Year: item.Subject.Year,
		Status: status, Rating: rating, Comment: comment, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
}

func allowedSubject(subject Subject) bool {
	return allowedKind(subject.Type) || allowedKind(subject.Subtype)
}

func allowedKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "movie", "tv", "show":
		return true
	default:
		return false
	}
}

func parseDoubanTime(value string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func defaultPageDelay(ctx context.Context) error {
	delay := time.Duration(1000+time.Now().UnixNano()%2000) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
