package playback

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/doubanpopular"
	"golang.org/x/sync/singleflight"
)

// PopularSubject 是热门榜里的一条，JSON 字段名要和前端、TVBox 保持兼容。
type PopularSubject struct {
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Rate               string         `json:"rate"`
	Cover              string         `json:"cover"`
	URL                string         `json:"url"`
	IsNew              bool           `json:"is_new"`
	EpisodesInfo       string         `json:"episodes_info"`
	Year string `json:"year,omitempty"`
}

// PopularSource 标识一种热门来源，来源按声明顺序决定优先级（靠前的优先）。
type PopularSource struct {
	Name     string
	Provider PopularProvider
}

// CompositePopularProvider 按优先级合并多个热门来源，先到先得去重。
// 单个来源故障只造成部分降级，其他成功来源仍可使用。
type CompositePopularProvider struct {
	sources []PopularSource
	mu      sync.Mutex
	cache   map[string]popularCacheEntry
	group   singleflight.Group
}

// NewCompositePopularProvider 创建组合来源，自动跳过没配置的来源。
func NewCompositePopularProvider(sources ...PopularSource) *CompositePopularProvider {
	valid := make([]PopularSource, 0, len(sources))
	for _, source := range sources {
		if source.Provider == nil {
			continue
		}
		if source.Name == "" {
			source.Name = "source"
		}
		valid = append(valid, source)
	}
	return &CompositePopularProvider{sources: valid, cache: make(map[string]popularCacheEntry)}
}

// Popular 返回融合后的热门榜（结果缓存 15 分钟）。
func (provider *CompositePopularProvider) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	value, err, _ := provider.group.Do(mediaType, func() (any, error) {
		return provider.loadPopular(ctx, mediaType)
	})
	if value == nil {
		return nil, err
	}
	return append([]PopularSubject(nil), value.([]PopularSubject)...), err
}

// loadPopular 按来源声明顺序合并：靠前的来源优先，重复条目只保留先出现的。
// 所有来源都失败时退回上一次的缓存，宁可旧也不要空榜。
func (provider *CompositePopularProvider) loadPopular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	provider.mu.Lock()
	cached, found := provider.cache[mediaType]
	if found && time.Now().Before(cached.expiresAt) {
		result := append([]PopularSubject(nil), cached.subjects...)
		provider.mu.Unlock()
		return result, nil
	}
	provider.mu.Unlock()

	var result []PopularSubject
	var lastErr error
	for _, source := range provider.sources {
		subjects, err := source.Provider.Popular(ctx, mediaType)
		if err != nil {
			slog.Warn("popularity source unavailable", "source", source.Name, "media_type", mediaType, "error", err)
			lastErr = fmt.Errorf("%s popularity source: %w", source.Name, err)
			continue
		}
		result = mergePopularSubjects(result, subjects, popularitySnapshotSize)
	}
	if len(result) == 0 {
		if found && len(cached.subjects) > 0 {
			return append([]PopularSubject(nil), cached.subjects...), nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return []PopularSubject{}, nil
	}
	provider.mu.Lock()
	provider.cache[mediaType] = popularCacheEntry{subjects: append([]PopularSubject(nil), result...), expiresAt: time.Now().Add(15 * time.Minute)}
	provider.mu.Unlock()
	return result, nil
}

// popularIdentity 去重键：优先用豆瓣 ID，没有就用「标题+年份」。
func popularIdentity(subject PopularSubject) string {
	if id := strings.TrimSpace(subject.ID); id != "" {
		return "id:" + id
	}
	title := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(subject.Title)), " "))
	if title == "" {
		return ""
	}
	return "title:" + title + ":" + strings.TrimSpace(subject.Year)
}

// popularResponse 是豆瓣热门接口的返回结构。
type popularResponse struct {
	Subjects []PopularSubject `json:"subjects"`
}

// popularCacheEntry 是带过期时间的内存缓存条目。
type popularCacheEntry struct {
	subjects  []PopularSubject
	expiresAt time.Time
}

// DoubanPopularProvider 抓豆瓣热门榜，结果缓存 12 小时。
type DoubanPopularProvider struct {
	client *http.Client
	mu     sync.RWMutex
	cache  map[string]popularCacheEntry
	group  singleflight.Group
}

// TMDBPopularProvider 抓 TMDB 热门榜，只保留能在站内找到对应豆瓣 ID 的条目。
type TMDBPopularProvider struct {
	client   *http.Client
	token    string
	resolver PopularIdentityResolver
	base     string
}

// NewTMDBPopularProvider 创建 TMDB 热门来源。
func NewTMDBPopularProvider(client *http.Client, token string, resolver PopularIdentityResolver) *TMDBPopularProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &TMDBPopularProvider{client: client, token: strings.TrimSpace(token), resolver: resolver, base: "https://api.themoviedb.org"}
}

// Popular 拉 TMDB 热门并映射到站内媒体。
func (provider *TMDBPopularProvider) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	if provider.token == "" {
		return nil, fmt.Errorf("TMDB_API_TOKEN is not configured")
	}
	if provider.resolver == nil {
		return nil, fmt.Errorf("TMDB popular resolver is not configured")
	}
	kind := "movie"
	if mediaType != "movie" {
		kind = "tv"
	}
	endpoint := fmt.Sprintf("%s/3/%s/popular?language=zh-CN&page=1", strings.TrimRight(provider.base, "/"), kind)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+provider.token)
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request TMDB popular: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB popular returned status %d", response.StatusCode)
	}
	var payload struct {
		Results []struct {
			ID           int     `json:"id"`
			Title        string  `json:"title"`
			Name         string  `json:"name"`
			PosterPath   string  `json:"poster_path"`
			VoteAverage  float64 `json:"vote_average"`
			ReleaseDate  string  `json:"release_date"`
			FirstAirDate string  `json:"first_air_date"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode TMDB popular: %w", err)
	}
	items := make([]PopularSubject, 0, len(payload.Results))
	for _, item := range payload.Results {
		media, err := provider.resolver.FindByExternalID(ctx, "tmdb", kind, strconv.Itoa(item.ID))
		if err != nil || media.DoubanID == "" {
			continue
		}
		title := item.Title
		if title == "" {
			title = item.Name
		}
		if title == "" {
			title = media.Title
		}
		year := item.ReleaseDate
		if year == "" {
			year = item.FirstAirDate
		}
		if len(year) > 4 {
			year = year[:4]
		}
		cover := ""
		if item.PosterPath != "" {
			cover = proxyImagePath("https://image.tmdb.org/t/p/w500" + item.PosterPath)
		}
		items = append(items, PopularSubject{ID: media.DoubanID, Title: title, Year: year, Rate: strconv.FormatFloat(item.VoteAverage, 'f', 1, 64), Cover: cover, URL: "https://www.themoviedb.org/" + url.PathEscape(kind) + "/" + strconv.Itoa(item.ID)})
	}
	return items, nil
}

// NewDoubanPopularProvider 创建豆瓣热门来源。
func NewDoubanPopularProvider(client *http.Client) *DoubanPopularProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &DoubanPopularProvider{client: client, cache: make(map[string]popularCacheEntry)}
}

// Popular 返回豆瓣热门榜。
func (provider *DoubanPopularProvider) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	value, err, _ := provider.group.Do(mediaType, func() (any, error) {
		return provider.loadPopular(ctx, mediaType)
	})
	if value == nil {
		return nil, err
	}
	return append([]PopularSubject(nil), value.([]PopularSubject)...), err
}

// loadPopular 抓豆瓣 search_subjects 接口，失败时依次尝试 Rexxar 接口和陈旧缓存。
func (provider *DoubanPopularProvider) loadPopular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	target, err := popularURL(mediaType)
	if err != nil {
		return nil, err
	}
	provider.mu.RLock()
	cached, found := provider.cache[mediaType]
	provider.mu.RUnlock()
	if found && time.Now().Before(cached.expiresAt) {
		return append([]PopularSubject(nil), cached.subjects...), nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create douban popular request: %w", err)
	}
	request.Header.Set("User-Agent", providerUserAgent)
	request.Header.Set("Referer", "https://movie.douban.com/")
	response, err := provider.client.Do(request)
	if err != nil {
		return provider.rexxarOrStale(ctx, mediaType, cached, found, fmt.Errorf("request douban popular: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return provider.rexxarOrStale(ctx, mediaType, cached, found, fmt.Errorf("douban popular returned status %d", response.StatusCode))
	}
	var payload popularResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return provider.rexxarOrStale(ctx, mediaType, cached, found, fmt.Errorf("decode douban popular: %w", err))
	}
	for index := range payload.Subjects {
		payload.Subjects[index].Cover = proxyImagePath(payload.Subjects[index].Cover)
	}
	provider.mu.Lock()
	provider.cache[mediaType] = popularCacheEntry{subjects: append([]PopularSubject(nil), payload.Subjects...), expiresAt: time.Now().Add(12 * time.Hour)}
	provider.mu.Unlock()
	return payload.Subjects, nil
}

// rexxarOrStale 是豆瓣主接口失败后的兜底：先试豆瓣 App 用的 Rexxar 接口，再退回旧缓存。
func (provider *DoubanPopularProvider) rexxarOrStale(ctx context.Context, mediaType string, cached popularCacheEntry, found bool, primaryErr error) ([]PopularSubject, error) {
	subjects, err := doubanpopular.FetchRexxar(ctx, provider.client, mediaType)
	if err == nil {
		mapped := make([]PopularSubject, 0, len(subjects))
		for _, subject := range subjects {
			mapped = append(mapped, PopularSubject{ID: subject.ID, Title: subject.Title, Rate: subject.Rate, Cover: proxyImagePath(subject.Cover), URL: subject.URL, EpisodesInfo: subject.EpisodesInfo})
		}
		provider.mu.Lock()
		provider.cache[mediaType] = popularCacheEntry{subjects: append([]PopularSubject(nil), mapped...), expiresAt: time.Now().Add(12 * time.Hour)}
		provider.mu.Unlock()
		return mapped, nil
	}
	if found {
		return append([]PopularSubject(nil), cached.subjects...), nil
	}
	return nil, fmt.Errorf("primary Douban popular: %v; Rexxar fallback: %w", primaryErr, err)
}

// popularURL 各分类对应的豆瓣榜单地址。
func popularURL(mediaType string) (string, error) {
	switch mediaType {
	case "movie":
		return "https://movie.douban.com/j/search_subjects?type=movie&tag=热门&page_limit=50&page_start=0", nil
	case "tv":
		return "https://movie.douban.com/j/search_subjects?type=tv&tag=热门&page_limit=50&page_start=0", nil
	case "show":
		return "https://movie.douban.com/j/search_subjects?type=tv&tag=综艺&page_limit=50&page_start=0", nil
	case "cartoon":
		return "https://movie.douban.com/j/search_subjects?type=tv&tag=日本动画&page_limit=50&page_start=0", nil
	default:
		return "", fmt.Errorf("unsupported media type %q", mediaType)
	}
}

// proxyImagePath 把外站图片地址转成本站图片代理路径（豆瓣图片有防盗链）。
func proxyImagePath(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	return "/api/proxy/image/r76RqSIVvUryzx" + base64.RawURLEncoding.EncodeToString([]byte(rawURL))
}

// providerUserAgent 抓豆瓣时伪装成普通浏览器。
const providerUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
