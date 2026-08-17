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
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"golang.org/x/sync/singleflight"
)

type DoubanProvider struct {
	client      *http.Client
	store       Store
	group       singleflight.Group
	base        string
	suggestBase string
	popularMu   sync.Mutex
	popular     map[string]popularCacheEntry
	canonical   CanonicalWriter
}

type DoubanOption func(*DoubanProvider)

func WithDoubanCanonicalWriter(writer CanonicalWriter) DoubanOption {
	return func(provider *DoubanProvider) { provider.canonical = writer }
}

func NewDoubanProvider(client *http.Client, store Store, options ...DoubanOption) *DoubanProvider {
	provider := &DoubanProvider{client: client, store: store, base: "https://m.douban.com", suggestBase: "https://movie.douban.com", popular: make(map[string]popularCacheEntry)}
	for _, option := range options {
		option(provider)
	}
	return provider
}

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

type popularCacheEntry struct {
	subjects  []PopularSubject
	expiresAt time.Time
}

func (provider *DoubanProvider) Popular(ctx context.Context, movieType string) ([]PopularSubject, error) {
	query := ""
	switch movieType {
	case "movie":
		query = "type=movie&tag=热门"
	case "tv":
		query = "type=tv&tag=热门"
	case "show":
		query = "type=tv&tag=综艺"
	case "cartoon":
		query = "type=tv&tag=日本动画"
	default:
		return nil, fmt.Errorf("unsupported movie type %q", movieType)
	}
	provider.popularMu.Lock()
	cached, exists := provider.popular[movieType]
	if exists && time.Now().Before(cached.expiresAt) {
		result := append([]PopularSubject(nil), cached.subjects...)
		provider.popularMu.Unlock()
		return result, nil
	}
	provider.popularMu.Unlock()

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
		if inferMovieType(movie.Genres) != movieType {
			continue
		}
		fallback = append(fallback, PopularSubject{ID: movie.DoubanID, Title: movie.Title, Rate: fmt.Sprintf("%.1f", movie.Rating), Cover: proxyImageURL(movie.Poster)})
	}
	if len(fallback) == 0 && err != nil {
		return nil, fmt.Errorf("primary Douban popular: %v; Rexxar fallback: %w", err, rexxarErr)
	}
	return fallback, nil
}

func (provider *DoubanProvider) cachePopular(movieType string, subjects []PopularSubject) {
	provider.popularMu.Lock()
	provider.popular[movieType] = popularCacheEntry{subjects: append([]PopularSubject(nil), subjects...), expiresAt: time.Now().Add(12 * time.Hour)}
	provider.popularMu.Unlock()
}

type Suggestion struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SubTitle string `json:"sub_title"`
	Type     string `json:"type"`
	Year     string `json:"year"`
	Episode  string `json:"episode"`
	Img      string `json:"img"`
}

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

func (provider *DoubanProvider) SuggestExternal(ctx context.Context, keyword string) ([]Suggestion, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []Suggestion{}, nil
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

func (provider *DoubanProvider) getJSON(ctx context.Context, endpoint, referer string, destination any) error {
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
		return classifyUpstreamStatus("Douban", response)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode Douban response: %w", err)
	}
	return nil
}

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
