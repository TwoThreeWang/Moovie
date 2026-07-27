package service

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
)

// DoubanSyncService 豆瓣观影记录同步服务
type DoubanSyncService struct {
	repos   *repository.Repositories
	crawler *DoubanCrawler
	client  *http.Client
}

// NewDoubanSyncService 创建同步服务
func NewDoubanSyncService(repos *repository.Repositories, crawler *DoubanCrawler) *DoubanSyncService {
	return &DoubanSyncService{
		repos:   repos,
		crawler: crawler,
		client:  utils.GlobalHttpClient,
	}
}

// ValidateUser 验证豆瓣用户是否存在且公开
func (s *DoubanSyncService) ValidateUser(ctx context.Context, doubanUserID string) error {
	url := fmt.Sprintf("https://m.douban.com/rexxar/api/v2/user/%s/interests?type=movie&status=mark&start=0&count=1&ck=&for_mobile=1", doubanUserID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	s.setRexxarHeaders(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("豆瓣用户不存在")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("豆瓣返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	var result rexxarInterestsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	return nil
}

// SyncFull 执行全量同步
func (s *DoubanSyncService) SyncFull(ctx context.Context, userID int, doubanUserID string, jobID int) error {
	types := []string{"movie", "tv"}
	statuses := []string{"mark", "done"}
	processed, failed := 0, 0

	log.Printf("[DoubanSync] 开始全量同步 (user=%d, douban=%s, job=%d)", userID, doubanUserID, jobID)
	totalAll := 0
	for _, itemType := range types {
		for _, status := range statuses {
			start := 0
			count := 20
			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("同步被取消")
				default:
				}

				url := fmt.Sprintf("https://m.douban.com/rexxar/api/v2/user/%s/interests?type=%s&status=%s&start=%d&count=%d&ck=&for_mobile=1", doubanUserID, itemType, status, start, count)
				items, total, err := s.fetchInterestsPage(ctx, url)
				if err != nil {
					_ = s.repos.DoubanSyncJob.UpdateStatus(jobID, model.DoubanSyncStatusFailed, err.Error())
					return err
				}

				if start == 0 {
					totalAll += total
					_ = s.repos.DoubanSyncJob.UpdateTotal(jobID, totalAll)
					log.Printf("[DoubanSync] type=%s status=%s total=%d 累计 total=%d (user=%d, job=%d)", itemType, status, total, totalAll, userID, jobID)
				}
				log.Printf("[DoubanSync] 获取 type=%s status=%s start=%d 返回 %d 条 (user=%d, job=%d)", itemType, status, start, len(items), userID, jobID)

				for _, item := range items {
					saved, err := s.syncInterest(userID, item)
					if err != nil {
						log.Printf("[DoubanSync] 保存兴趣失败 (user=%d, subject=%s): %v", userID, item.Subject.ID.String(), err)
						failed++
					} else if saved {
						processed++
					}
				}

				_ = s.repos.DoubanSyncJob.UpdateProgress(jobID, processed, failed, strconv.Itoa(start+len(items)))

				// 以 total 作为分页边界，避免 API 返回的 count 字段异常导致提前结束
				if len(items) == 0 || start+len(items) >= total {
					log.Printf("[DoubanSync] type=%s status=%s 分页结束 start=%d len=%d total=%d (user=%d, job=%d)", itemType, status, start, len(items), total, userID, jobID)
					break
				}
				start += count

				// 反爬：每页之间随机休息 1-3 秒
				time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)
			}
		}
	}

	log.Printf("[DoubanSync] 全量同步完成 (user=%d, job=%d, processed=%d, failed=%d)", userID, jobID, processed, failed)
	return s.repos.DoubanSyncJob.UpdateStatus(jobID, model.DoubanSyncStatusCompleted, "")
}

// SyncIncremental 执行 RSS 增量同步
func (s *DoubanSyncService) SyncIncremental(ctx context.Context, userID int, doubanUserID string, jobID int) error {
	rssItems, earliestTime, err := s.fetchRSSItems(ctx, doubanUserID)
	if err != nil {
		_ = s.repos.DoubanSyncJob.UpdateStatus(jobID, model.DoubanSyncStatusFailed, err.Error())
		return err
	}

	if len(rssItems) == 0 {
		return s.repos.DoubanSyncJob.UpdateStatus(jobID, model.DoubanSyncStatusCompleted, "")
	}

	subjectSet := make(map[string]bool)
	for _, item := range rssItems {
		subjectSet[item.SubjectID] = true
	}

	processed, failed := 0, 0
	types := []string{"movie", "tv"}
	statuses := []string{"mark", "done"}
	for _, itemType := range types {
		for _, status := range statuses {
			start := 0
			count := 20
			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("同步被取消")
				default:
				}

				url := fmt.Sprintf("https://m.douban.com/rexxar/api/v2/user/%s/interests?type=%s&status=%s&start=%d&count=%d&ck=&for_mobile=1", doubanUserID, itemType, status, start, count)
				items, _, err := s.fetchInterestsPage(ctx, url)
				if err != nil {
					_ = s.repos.DoubanSyncJob.UpdateStatus(jobID, model.DoubanSyncStatusFailed, err.Error())
					return err
				}

				outOfRange := false
				for _, item := range items {
					itemTime, _ := time.Parse("2006-01-02 15:04:05", item.CreateTime)
					if !itemTime.IsZero() && itemTime.Before(earliestTime) {
						outOfRange = true
						break
					}
					if subjectSet[item.Subject.ID.String()] {
						saved, err := s.syncInterest(userID, item)
						if err != nil {
							log.Printf("[DoubanSync] 保存兴趣失败 (user=%d, subject=%s): %v", userID, item.Subject.ID.String(), err)
							failed++
						} else if saved {
							processed++
						}
					}
				}

				_ = s.repos.DoubanSyncJob.UpdateProgress(jobID, processed, failed, strconv.Itoa(start+len(items)))

				if outOfRange || len(items) == 0 {
					break
				}
				start += count
				time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)
			}
		}
	}

	log.Printf("[DoubanSync] 增量同步完成 (user=%d, job=%d, processed=%d, failed=%d)", userID, jobID, processed, failed)
	_ = s.repos.DoubanSyncJob.UpdateProgress(jobID, processed, failed, "")
	return s.repos.DoubanSyncJob.UpdateStatus(jobID, model.DoubanSyncStatusCompleted, "")
}

// fetchInterestsPage 获取 Rexxar 兴趣列表的一页
func (s *DoubanSyncService) fetchInterestsPage(ctx context.Context, url string) ([]rexxarInterest, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	s.setRexxarHeaders(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("Rexxar 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	var result rexxarInterestsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("解析 Rexxar 响应失败: %w", err)
	}

	return result.Interests, result.Total, nil
}

func (s *DoubanSyncService) syncInterest(userID int, item rexxarInterest) (bool, error) {
	if !isAllowedDoubanSyncSubject(item.Subject) {
		log.Printf("[DoubanSync] 跳过非影视兴趣 (user=%d, subject=%s, type=%s, subtype=%s, title=%s)",
			userID, item.Subject.ID.String(), item.Subject.Type, item.Subject.Subtype, item.Subject.Title)
		return false, nil
	}

	if err := s.upsertInterest(userID, item); err != nil {
		return false, err
	}
	return true, nil
}

func isAllowedDoubanSyncSubject(subject rexxarSubject) bool {
	return isAllowedDoubanSyncKind(subject.Type) || isAllowedDoubanSyncKind(subject.Subtype)
}

func isAllowedDoubanSyncKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "movie", "tv", "show":
		return true
	default:
		return false
	}
}

// upsertInterest 将单个兴趣保存到 user_movies
func (s *DoubanSyncService) upsertInterest(userID int, item rexxarInterest) error {
	status := "wish"
	if item.Status == "done" {
		status = "watched"
	}

	rating := 0
	if item.Rating != nil {
		rating = int(item.Rating.Value)
	}

	// 如果本地已有记录且本地有评分/评论而豆瓣没有，保留本地
	subjectID := item.Subject.ID.String()
	existing, err := s.repos.UserMovie.GetByUserAndMovie(userID, subjectID)
	if err == nil && existing != nil {
		if rating == 0 && existing.Rating > 0 {
			rating = existing.Rating
		}
		if item.Comment == "" && existing.Comment != "" {
			item.Comment = existing.Comment
		}
	}

	// 使用豆瓣的 create_time 作为创建时间
	var createdAt time.Time
	if item.CreateTime != "" {
		// 尝试解析不同格式的时间
		createdAt, _ = time.Parse("2006-01-02 15:04:05", item.CreateTime)
		if createdAt.IsZero() {
			createdAt, _ = time.Parse("2006-01-02", item.CreateTime)
		}
	}

	record := &model.UserMovie{
		UserID:    userID,
		MovieID:   subjectID,
		Title:     item.Subject.Title,
		Poster:    item.Subject.CoverURL,
		Year:      item.Subject.Year,
		Status:    status,
		Rating:    rating,
		Comment:   item.Comment,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	return s.repos.UserMovie.Upsert(record)
}

// setRexxarHeaders 设置 Rexxar API 请求头
func (s *DoubanSyncService) setRexxarHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Referer", "https://m.douban.com/")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf(`ll="108288"; bid=%s`, s.crawler.generateBid()))
}

// setRSSHeaders 设置 RSS 请求头
func (s *DoubanSyncService) setRSSHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/rss+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", "https://www.douban.com/")
}

// fetchRSSItems 获取并解析豆瓣兴趣 RSS，返回条目列表与最早发布时间
func (s *DoubanSyncService) fetchRSSItems(ctx context.Context, doubanUserID string) ([]rssInterest, time.Time, error) {
	url := fmt.Sprintf("https://www.douban.com/feed/people/%s/interests", doubanUserID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.setRSSHeaders(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, time.Time{}, fmt.Errorf("RSS 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, time.Time{}, err
	}

	var feed rssFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, time.Time{}, fmt.Errorf("解析 RSS 失败: %w", err)
	}

	var items []rssInterest
	var earliestTime time.Time
	for _, item := range feed.Channel.Items {
		subjectID := extractSubjectID(item.Link)
		if subjectID == "" {
			continue
		}
		pubTime, _ := time.Parse(time.RFC1123, item.PubDate)
		if pubTime.IsZero() {
			pubTime, _ = time.Parse(time.RFC1123Z, item.PubDate)
		}
		if !pubTime.IsZero() {
			if earliestTime.IsZero() || pubTime.Before(earliestTime) {
				earliestTime = pubTime
			}
		}
		items = append(items, rssInterest{
			SubjectID: subjectID,
			Title:     item.Title,
			Link:      item.Link,
			PubDate:   item.PubDate,
			PubTime:   pubTime,
		})
	}

	// 留一点余量：再往前推 1 天，防止时区或解析误差导致漏数据
	if !earliestTime.IsZero() {
		earliestTime = earliestTime.Add(-24 * time.Hour)
	}

	return items, earliestTime, nil
}

// extractSubjectID 从豆瓣链接中提取 subject ID
func extractSubjectID(link string) string {
	re := regexp.MustCompile(`/(?:subject|movie|tv)/(\d+)`)
	matches := re.FindStringSubmatch(link)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// rexxarInterestsResponse Rexxar 用户兴趣列表响应
type rexxarInterestsResponse struct {
	Start     int              `json:"start"`
	Count     int              `json:"count"`
	Total     int              `json:"total"`
	Interests []rexxarInterest `json:"interests"`
}

type rexxarInterest struct {
	ID      json.Number `json:"id"`
	Status  string      `json:"status"`
	Comment string      `json:"comment"`
	Rating  *struct {
		Value float64 `json:"value"`
	} `json:"rating"`
	CreateTime string        `json:"create_time"`
	Subject    rexxarSubject `json:"subject"`
}

type rexxarSubject struct {
	ID       json.Number `json:"id"`
	Title    string      `json:"title"`
	Type     string      `json:"type"`
	Subtype  string      `json:"subtype"`
	Year     string      `json:"year"`
	CoverURL string      `json:"cover_url"`
	Pic      struct {
		Large  string `json:"large"`
		Normal string `json:"normal"`
	} `json:"pic"`
}

type rssInterest struct {
	SubjectID string
	Title     string
	Link      string
	PubDate   string
	PubTime   time.Time
}
