package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/doubanpopular"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"golang.org/x/sync/singleflight"
)

// defaultDoubanRequestInterval 是没有配置时的豆瓣请求最小间隔。
// worker 并发一高，多个任务会同时把请求打到同一个出口 IP 上触发豆瓣的频率限制，
// 这里把全进程的豆瓣请求串行化，出问题时 Pause 还会让大家一起冷却。
const defaultDoubanRequestInterval = 200 * time.Millisecond

// DoubanProvider 抓取豆瓣的主资料、短评、热门榜和搜索联想。
// singleflight 合并同一条目的并发抓取，limiter 保证全进程对豆瓣的请求不超频。
type DoubanProvider struct {
	client      *http.Client
	store       Store
	group       singleflight.Group
	base        string
	suggestBase string
	popularMu   sync.Mutex
	popular     map[string]popularCacheEntry
	canonical   CanonicalWriter
	limiter     *outbound.Limiter
}

// DoubanOption 是豆瓣抓取器的可选装配项。
type DoubanOption func(*DoubanProvider)

// WithDoubanCanonicalWriter 注入规范媒体写入器。
func WithDoubanCanonicalWriter(writer CanonicalWriter) DoubanOption {
	return func(provider *DoubanProvider) { provider.canonical = writer }
}

// WithDoubanRequestInterval 覆盖豆瓣请求的最小间隔。
func WithDoubanRequestInterval(interval time.Duration) DoubanOption {
	return func(provider *DoubanProvider) { provider.limiter = outbound.NewLimiter(interval) }
}

// NewDoubanProvider 创建豆瓣抓取器。
func NewDoubanProvider(client *http.Client, store Store, options ...DoubanOption) *DoubanProvider {
	provider := &DoubanProvider{client: client, store: store, base: "https://m.douban.com", suggestBase: "https://movie.douban.com",
		popular: make(map[string]popularCacheEntry), limiter: outbound.NewLimiter(defaultDoubanRequestInterval)}
	for _, option := range options {
		option(provider)
	}
	return provider
}

// PopularSubject 是发现页热门榜的一条数据。
type PopularSubject struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Rate         string `json:"rate"`
	Cover        string `json:"cover"`
	URL          string `json:"url"`
	IsNew        bool   `json:"is_new"`
	EpisodesInfo string `json:"episodes_info"`
}

// HasRating 只展示真实的正数评分，避免把上游的“0.0”误解成低分作品。
func (subject PopularSubject) HasRating() bool {
	rating, err := strconv.ParseFloat(strings.TrimSpace(subject.Rate), 64)
	return err == nil && rating > 0
}

// popularCacheEntry 是热门榜的内存缓存条目。
type popularCacheEntry struct {
	subjects  []PopularSubject
	expiresAt time.Time
}

// popularQueries 是发现页四个分类对应的豆瓣查询参数。
var popularQueries = map[string]string{
	"movie":   "type=movie&tag=热门",
	"tv":      "type=tv&tag=热门",
	"show":    "type=tv&tag=综艺",
	"cartoon": "type=tv&tag=日本动画",
}

// popularRefreshTimeout 是后台刷新热门榜的整体上限，防止后台 goroutine 挂死。
const popularRefreshTimeout = 30 * time.Second

// Popular 取热门榜，缓存 12 小时。
// 缓存新鲜就直接返回；缓存过期但旧榜单还在，先把旧的还给页面、刷新丢到后台跑，
// 因为同步等上游最坏要 20 秒（主接口超时 + Rexxar 兜底再超时一次），
// 那 20 秒里发现页就是一个空转的加载圈，而热门榜晚几分钟根本没人看得出来。
// 只有完全没有缓存的冷启动才同步等一次。
func (provider *DoubanProvider) Popular(ctx context.Context, movieType string) ([]PopularSubject, error) {
	if _, supported := popularQueries[movieType]; !supported {
		return nil, fmt.Errorf("unsupported movie type %q", movieType)
	}
	cached, exists := provider.cachedPopular(movieType)
	if exists && time.Now().Before(cached.expiresAt) {
		return append([]PopularSubject(nil), cached.subjects...), nil
	}
	if exists && len(cached.subjects) > 0 {
		provider.refreshPopularInBackground(ctx, movieType)
		return append([]PopularSubject(nil), cached.subjects...), nil
	}
	return provider.refreshPopular(ctx, movieType)
}

// cachedPopular 读一份热门榜缓存，包括已经过期的。
func (provider *DoubanProvider) cachedPopular(movieType string) (popularCacheEntry, bool) {
	provider.popularMu.Lock()
	defer provider.popularMu.Unlock()
	entry, exists := provider.popular[movieType]
	return entry, exists
}

// refreshPopularInBackground 把刷新丢到后台。用 WithoutCancel 保留日志上下文，
// 但请求结束后不能跟着被取消，否则后台刷新永远跑不完，缓存也就永远不会更新。
func (provider *DoubanProvider) refreshPopularInBackground(ctx context.Context, movieType string) {
	detached := context.WithoutCancel(ctx)
	go func() {
		background, cancel := context.WithTimeout(detached, popularRefreshTimeout)
		defer cancel()
		_, _ = provider.refreshPopular(background, movieType)
	}()
}

// refreshPopular 真正去抓热门榜。降级顺序：主接口 → Rexxar 接口 → 过期缓存 → 本地高分库。
// 用 singleflight 合并并发刷新：冷启动时四个分类被同时打开，同一分类只该发一次请求。
func (provider *DoubanProvider) refreshPopular(ctx context.Context, movieType string) ([]PopularSubject, error) {
	result, err, _ := provider.group.Do("popular:"+movieType, func() (any, error) {
		return provider.fetchPopular(ctx, movieType)
	})
	if err != nil {
		return nil, err
	}
	return result.([]PopularSubject), nil
}

// fetchPopular 是热门榜抓取和各级降级的实现，只应由 refreshPopular 调用。
func (provider *DoubanProvider) fetchPopular(ctx context.Context, movieType string) ([]PopularSubject, error) {
	query := popularQueries[movieType]
	cached, exists := provider.cachedPopular(movieType)

	endpoint := strings.TrimRight(provider.suggestBase, "/") + "/j/search_subjects?" + query + "&page_limit=50&page_start=0"
	var response struct {
		Subjects []PopularSubject `json:"subjects"`
	}
	err := provider.getJSON(ctx, endpoint, "https://movie.douban.com/", &response)
	if err == nil {
		for index := range response.Subjects {
			response.Subjects[index].Cover = proxyImageURL(response.Subjects[index].Cover)
		}
		provider.cachePopular(movieType, response.Subjects)
		return response.Subjects, nil
	}
	rexxarSubjects, rexxarErr := doubanpopular.FetchRexxar(ctx, provider.client, movieType)
	if rexxarErr == nil {
		mapped := make([]PopularSubject, 0, len(rexxarSubjects))
		for _, subject := range rexxarSubjects {
			mapped = append(mapped, PopularSubject{ID: subject.ID, Title: subject.Title, Rate: subject.Rate, Cover: proxyImageURL(subject.Cover), URL: subject.URL, EpisodesInfo: subject.EpisodesInfo})
		}
		provider.cachePopular(movieType, mapped)
		return mapped, nil
	}
	if exists && len(cached.subjects) > 0 {
		return append([]PopularSubject(nil), cached.subjects...), nil
	}
	local, localErr := provider.store.Popular(ctx, 50)
	if localErr != nil {
		return nil, err
	}
	fallback := make([]PopularSubject, 0)
	for _, movie := range local {
		if fallbackMovieType(movie) != movieType {
			continue
		}
		fallback = append(fallback, PopularSubject{ID: movie.DoubanID, Title: movie.Title, Rate: fmt.Sprintf("%.1f", movie.Rating), Cover: proxyImageURL(movie.Poster)})
	}
	if len(fallback) == 0 && err != nil {
		return nil, fmt.Errorf("primary Douban popular: %v; Rexxar fallback: %w", err, rexxarErr)
	}
	return fallback, nil
}

// cachePopular 写入热门榜缓存。
func (provider *DoubanProvider) cachePopular(movieType string, subjects []PopularSubject) {
	provider.popularMu.Lock()
	provider.popular[movieType] = popularCacheEntry{subjects: append([]PopularSubject(nil), subjects...), expiresAt: time.Now().Add(12 * time.Hour)}
	provider.popularMu.Unlock()
}

// Suggestion 是一条搜索联想结果。
type Suggestion struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SubTitle string `json:"sub_title"`
	Type     string `json:"type"`
	Year     string `json:"year"`
	Episode  string `json:"episode"`
	Img      string `json:"img"`
}

// rexxarMovie 对应豆瓣移动端 rexxar 接口的影片详情结构。
type rexxarMovie struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title"`
	AKA           []string `json:"aka"`
	Year          string   `json:"year"`
	Intro         string   `json:"intro"`
	CoverURL      string   `json:"cover_url"`
	Genres        []string `json:"genres"`
	Countries     []string `json:"countries"`
	Durations     []string `json:"durations"`
	Rating        struct {
		Value float64 `json:"value"`
	} `json:"rating"`
	Pic struct {
		Large string `json:"large"`
	} `json:"pic"`
	Directors []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"directors"`
	Actors []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"actors"`
}

// Fetch 抓一部影片的主资料。豆瓣按 movie/tv/show 分了三个端点，且事先不知道是哪一种，
// 所以依次尝试，第一个成功的即为准，同时把它的媒体类型作为规范类型写进 media 表。
func (provider *DoubanProvider) Fetch(ctx context.Context, doubanID string, _ bool) error {
	if !validDoubanID(doubanID) {
		return workqueue.Terminal(fmt.Errorf("invalid Douban ID %q", doubanID))
	}
	_, err, _ := provider.group.Do("movie:"+doubanID, func() (any, error) {
		var attempts mediaTypeAttempts
		for _, mediaType := range []string{"movie", "tv", "show"} {
			endpoint := fmt.Sprintf("%s/rexxar/api/v2/%s/%s?ck=&for_mobile=1", strings.TrimRight(provider.base, "/"), mediaType, url.PathEscape(doubanID))
			var response rexxarMovie
			if err := provider.getJSON(ctx, endpoint, "https://m.douban.com/", &response); err != nil {
				attempts.add(mediaType, err)
				continue
			}
			if response.ID == "" || response.Title == "" {
				attempts.add(mediaType, errors.New("empty response"))
				continue
			}
			movie := mapRexxarMovie(response)
			if err := provider.store.Upsert(ctx, movie); err != nil {
				return nil, fmt.Errorf("save Douban movie: %w", err)
			}
			mediaID, syncErr := syncCanonical(ctx, provider.canonical, movie, canonicalDoubanMediaType(mediaType), "douban", response,
				mediaidentity.ExternalID{Provider: "douban", ExternalID: movie.DoubanID, IsPrimary: true})
			if syncErr != nil {
				// 分阶段迁移期间继续保留旧豆瓣同步路径。
				return nil, nil
			}
			if aliasWriter, ok := provider.canonical.(mediaidentity.AliasWriter); ok {
				for _, alias := range response.AKA {
					if err := aliasWriter.UpsertAlias(ctx, mediaidentity.Alias{MediaID: mediaID, Alias: alias, Source: "douban", AliasType: "aka"}); err != nil {
						return nil, fmt.Errorf("save Douban alias: %w", err)
					}
				}
			}
			return nil, nil
		}
		return nil, attempts.err("fetch Douban movie " + doubanID)
	})
	return err
}

// FetchReviews 抓热门短评（最多 10 条），同样要三个端点轮着试。
func (provider *DoubanProvider) FetchReviews(ctx context.Context, doubanID string) error {
	if !validDoubanID(doubanID) {
		return workqueue.Terminal(fmt.Errorf("invalid Douban ID %q", doubanID))
	}
	_, err, _ := provider.group.Do("reviews:"+doubanID, func() (any, error) {
		var attempts mediaTypeAttempts
		for _, mediaType := range []string{"movie", "tv", "show"} {
			endpoint := fmt.Sprintf("%s/rexxar/api/v2/%s/%s/interests?count=10&order_by=hot&anony=0&start=0&ck=&for_mobile=1", strings.TrimRight(provider.base, "/"), mediaType, url.PathEscape(doubanID))
			var response struct {
				Interests []struct {
					Comment    string `json:"comment"`
					CreateTime string `json:"create_time"`
					SharingURL string `json:"sharing_url"`
					User       struct {
						Name string `json:"name"`
					} `json:"user"`
				} `json:"interests"`
			}
			if err := provider.getJSON(ctx, endpoint, fmt.Sprintf("https://m.douban.com/movie/subject/%s/", doubanID), &response); err != nil {
				attempts.add(mediaType, err)
				continue
			}
			reviews := make([]Review, 0, len(response.Interests))
			for _, interest := range response.Interests {
				reviews = append(reviews, Review{Title: interest.Comment, Summary: interest.Comment, Author: interest.User.Name, Link: interest.SharingURL, Published: interest.CreateTime})
			}
			encoded, err := json.Marshal(reviews)
			if err != nil {
				return nil, err
			}
			movie, err := provider.store.FindByDoubanID(ctx, doubanID)
			if err != nil {
				return nil, err
			}
			if movie == nil {
				return nil, fmt.Errorf("movie %s does not exist", doubanID)
			}
			movie.ReviewsJSON = string(encoded)
			movie.ReviewsUpdatedAt = time.Now()
			if err := provider.store.Upsert(ctx, *movie); err != nil {
				return nil, fmt.Errorf("save Douban reviews: %w", err)
			}
			return nil, nil
		}
		return nil, attempts.err("fetch Douban reviews " + doubanID)
	})
	return err
}

// mediaTypeAttempts 汇总 movie / tv / show 三个 rexxar 端点的失败原因。
// 原来只保留最后一条错误，排查时信息全丢：「movie 404 但 tv 403」和「三个都 404」
// 是完全不同的两回事，前者是媒体类型判断问题，后者是条目不存在或被风控。
type mediaTypeAttempts struct {
	failures []string
	errors   []error
}

// add 记录一次失败的尝试。
func (attempts *mediaTypeAttempts) add(mediaType string, err error) {
	attempts.failures = append(attempts.failures, mediaType+": "+err.Error())
	attempts.errors = append(attempts.errors, err)
}

// err 汇总所有端点的失败，并在全部返回 404 时判定为终止错误——
// 条目在三种媒体类型下都不存在，退避 24 小时再试也不会变出来。
func (attempts *mediaTypeAttempts) err(action string) error {
	if len(attempts.errors) == 0 {
		return fmt.Errorf("%s: no endpoint attempted", action)
	}
	combined := fmt.Errorf("%s: %s", action, strings.Join(attempts.failures, "; "))
	allNotFound := true
	for _, err := range attempts.errors {
		if status, ok := upstreamStatus(err); !ok || status != http.StatusNotFound {
			allNotFound = false
		}
		// 任何一个端点报限流，整体就按限流处理：这一轮的结论不可信。
		if retryAfter, throttled := workqueue.RetryAfter(err); throttled {
			return workqueue.Throttled(combined, retryAfter)
		}
	}
	if allNotFound {
		return workqueue.Terminal(combined)
	}
	return combined
}

// Suggest 先查本地库，本地没有才去问豆瓣。
func (provider *DoubanProvider) Suggest(ctx context.Context, keyword string) ([]Suggestion, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []Suggestion{}, nil
	}
	local, err := provider.store.Suggest(ctx, keyword, 10)
	if err == nil && len(local) > 0 {
		results := make([]Suggestion, 0, len(local))
		for _, movie := range local {
			results = append(results, Suggestion{ID: movie.DoubanID, Title: movie.Title, SubTitle: movie.OriginalTitle, Type: inferMovieType(movie.Genres), Year: movie.Year, Img: movie.Poster})
		}
		return results, nil
	}
	return provider.SuggestExternal(ctx, keyword)
}

// SuggestExternal 直接调豆瓣的联想接口。
// 走 limiter 是必须的：搜索页每次本地不足 5 条就会打一次这个接口，
// 不限速迟早被豆瓣 429，而 429 之后所有豆瓣抓取都会一起变慢。
// limiter.Wait 认 ctx，所以调用方给的超时仍然说了算。
func (provider *DoubanProvider) SuggestExternal(ctx context.Context, keyword string) ([]Suggestion, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []Suggestion{}, nil
	}
	if err := provider.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(provider.suggestBase, "/") + "/j/subject_suggest?q=" + url.QueryEscape(keyword)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request Douban suggestions: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Douban suggestions returned HTTP %d", response.StatusCode)
	}
	var external []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		SubTitle string `json:"sub_title"`
		Type     string `json:"type"`
		Year     string `json:"year"`
		Episode  string `json:"episode"`
		Img      string `json:"img"`
	}
	if err := json.NewDecoder(response.Body).Decode(&external); err != nil {
		return nil, fmt.Errorf("decode Douban suggestions: %w", err)
	}
	results := make([]Suggestion, 0, len(external))
	for _, item := range external {
		results = append(results, Suggestion{ID: item.ID, Title: item.Title, SubTitle: item.SubTitle, Type: item.Type, Year: item.Year, Episode: item.Episode, Img: proxyImageURL(item.Img)})
	}
	return results, nil
}

// getJSON 是所有豆瓣请求的公共出口：先等限流放行，被限流时让整个进程一起冷却。
func (provider *DoubanProvider) getJSON(ctx context.Context, endpoint, referer string, destination any) error {
	if err := provider.limiter.Wait(ctx); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
	request.Header.Set("Referer", referer)
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err := classifyUpstreamStatus("Douban", response)
		if retryAfter, throttled := workqueue.RetryAfter(err); throttled {
			if retryAfter <= 0 {
				retryAfter = 30 * time.Second
			}
			provider.limiter.Pause(retryAfter)
		}
		return err
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode Douban response: %w", err)
	}
	return nil
}

// validDoubanID 豆瓣 ID 必须是 6~9 位纯数字。
func validDoubanID(value string) bool {
	if len(value) < 6 || len(value) > 9 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// mapRexxarMovie 把豆瓣返回的结构映射成 Movie，导演和演员存成 JSON 字符串。
func mapRexxarMovie(response rexxarMovie) Movie {
	movie := Movie{
		DoubanID: response.ID, Title: response.Title, OriginalTitle: response.OriginalTitle,
		Year: response.Year, Summary: response.Intro, Rating: response.Rating.Value,
		Genres: strings.Join(response.Genres, ","), Countries: strings.Join(response.Countries, ","),
		Poster: response.Pic.Large,
	}
	if movie.Poster == "" {
		movie.Poster = response.CoverURL
	}
	if len(response.Durations) > 0 {
		movie.Duration = response.Durations[0]
	}
	directors := make([]Director, 0, len(response.Directors))
	for _, director := range response.Directors {
		directors = append(directors, Director{ID: director.ID, Name: director.Name})
	}
	actors := make([]Director, 0, len(response.Actors))
	for _, actor := range response.Actors {
		actors = append(actors, Director{ID: actor.ID, Name: actor.Name})
	}
	directorJSON, _ := json.Marshal(directors)
	actorJSON, _ := json.Marshal(actors)
	movie.Directors, movie.Actors = string(directorJSON), string(actorJSON)
	return movie
}

// fallbackMovieType 是本地数据库兜底专用的分类器。豆瓣真实的 genres 字段
// 只是「剧情/动作/悬疑」这类内容标签，几乎不会出现「电视剧」「综艺」字样，
// inferMovieType 在这些词上的匹配对本地库里的真实数据基本不命中——直接拿它给
// 兜底分组会导致电影和非电影混进同一个池子。这里先用可靠的 media.media_type
// （电影/非电影二分，写入时来自豆瓣详情端点，比猜内容标签靠谱）做硬隔离，
// 保证电影绝不会串进剧集/综艺/动漫，反之亦然；非电影桶内部仍用 inferMovieType
// 按关键词细分剧集/综艺/动漫，但匹配不到时归入剧集而不是电影。
func fallbackMovieType(movie Movie) string {
	if movie.MediaType != "tv" {
		return "movie"
	}
	if subType := inferMovieType(movie.Genres); subType != "movie" {
		return subType
	}
	return "tv"
}

// inferMovieType 从类型标签里猜是剧集/综艺/动漫，猜不出算电影。
// 注意：豆瓣的 genres 多是「剧情/动作」这种内容标签，这个函数命中率不高，
// 只适合做二次细分，不能单独用来判断媒体类型（见 fallbackMovieType）。
func inferMovieType(genres string) string {
	lower := strings.ToLower(genres)
	switch {
	case strings.Contains(lower, "电视剧"):
		return "tv"
	case strings.Contains(lower, "综艺"):
		return "show"
	case strings.Contains(lower, "动画") || strings.Contains(lower, "动漫"):
		return "cartoon"
	default:
		return "movie"
	}
}

// 豆瓣详情端点是规范媒体身份的权威来源。类型标签属于展示元数据，
// 经常省略“电视剧”等词，不能据此判断，否则剧集可能被静默归类为电影。
func canonicalDoubanMediaType(endpointType string) string {
	if strings.EqualFold(strings.TrimSpace(endpointType), "movie") {
		return "movie"
	}
	return "tv"
}
