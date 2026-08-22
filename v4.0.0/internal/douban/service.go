package douban

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

// InterestProvider 是同步逻辑依赖的豆瓣数据来源，测试时可替换。
type InterestProvider interface {
	ValidateUser(ctx context.Context, doubanUserID string) error
	Interests(ctx context.Context, doubanUserID, itemType, status string, start, count int) ([]Interest, int, error)
	RSSSubjects(ctx context.Context, doubanUserID string) (map[string]bool, time.Time, error)
}

// Service 执行同步。每页 20 条，翻页之间有延迟，避免请求太密被豆瓣限流。
type Service struct {
	provider InterestProvider
	library  library.Store
	jobs     JobStore
	pageSize int
	delay    func(context.Context) error
}

// ServiceOption 用于替换翻页延迟（测试时置空以免拖慢）。
type ServiceOption func(*Service)

// WithPageDelay 替换翻页延迟。
func WithPageDelay(delay func(context.Context) error) ServiceOption {
	return func(service *Service) { service.delay = delay }
}

// NewService 创建同步服务。
func NewService(provider InterestProvider, libraryStore library.Store, jobs JobStore, options ...ServiceOption) *Service {
	service := &Service{provider: provider, library: libraryStore, jobs: jobs, pageSize: 20, delay: defaultPageDelay}
	for _, option := range options {
		option(service)
	}
	return service
}

// ValidateUser 校验豆瓣 ID。
func (service *Service) ValidateUser(ctx context.Context, doubanUserID string) error {
	return service.provider.ValidateUser(ctx, doubanUserID)
}

// SyncFull 全量同步：电影和剧集 × 想看和看过，共四组分页全部翻完。
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

// SyncIncremental 增量同步：先从 RSS 拿到最近变动的条目，
// 再翻分页直到走过 RSS 中最早的时间点为止，比全量省很多请求。
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

// persistPage 保存一页数据，单条失败只计数不中断整页。
func (service *Service) persistPage(ctx context.Context, userID int, items []Interest, processed, failed *int) {
	for _, item := range items {
		service.persistInterest(ctx, userID, item, processed, failed)
	}
}

// persistInterest 保存单条标记。
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

// upsertInterest 把豆瓣标记写成本站片单记录。
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

// allowedSubject 过滤掉本站不收录的条目类型（比如图书、音乐）。
func allowedSubject(subject Subject) bool {
	return allowedKind(subject.Type) || allowedKind(subject.Subtype)
}

// allowedKind 判断条目类型是否属于影视。
func allowedKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "movie", "tv", "show":
		return true
	default:
		return false
	}
}

// parseDoubanTime 解析豆瓣的时间格式（东八区，无时区标记）。
func parseDoubanTime(value string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// defaultPageDelay 是默认的翻页间隔。
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
