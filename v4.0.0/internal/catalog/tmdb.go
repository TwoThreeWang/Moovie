package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"golang.org/x/sync/singleflight"
)

const (
	defaultWMDBBase = "https://api.wmdb.tv"
	defaultTMDBBase = "https://api.themoviedb.org"
	// wmdb 是免费的豆瓣→IMDb 映射服务，限流很紧。多个 worker 同时抢 tmdb 任务时，
	// 没有配速就会被整片拒绝，所以默认按最小间隔串行发送。
	defaultIMDbLookupInterval = 1200 * time.Millisecond
)

var errTMDBResultNotFound = errors.New("TMDB result not found")

// MediaUnitWriter 把结构化季集数据写入规范 media_units 表；为 nil 时跳过电视剧季集同步。
type MediaUnitWriter interface {
	EnsureMediaUnit(ctx context.Context, unit mediaidentity.MediaUnit) (mediaidentity.MediaUnit, error)
}

type TMDBProvider struct {
	client    *http.Client
	store     Store
	token     string
	wmdbBase  string
	tmdbBase  string
	group     singleflight.Group
	canonical CanonicalWriter
	units     MediaUnitWriter
	lookup    *outbound.Limiter
}

type TMDBOption func(*TMDBProvider)

func WithTMDBCanonicalWriter(writer CanonicalWriter) TMDBOption {
	return func(provider *TMDBProvider) { provider.canonical = writer }
}

func WithTMDBMediaUnitWriter(writer MediaUnitWriter) TMDBOption {
	return func(provider *TMDBProvider) { provider.units = writer }
}

func WithTMDBBases(wmdbBase, tmdbBase string) TMDBOption {
	return func(provider *TMDBProvider) {
		provider.wmdbBase = strings.TrimRight(wmdbBase, "/")
		provider.tmdbBase = strings.TrimRight(tmdbBase, "/")
	}
}

// WithTMDBIMDbLookupInterval 覆盖 wmdb 查询的最小发送间隔，传 0 表示不限速（仅测试使用）。
func WithTMDBIMDbLookupInterval(interval time.Duration) TMDBOption {
	return func(provider *TMDBProvider) { provider.lookup = outbound.NewLimiter(interval) }
}

func NewTMDBProvider(client *http.Client, store Store, token string, options ...TMDBOption) *TMDBProvider {
	provider := &TMDBProvider{
		client: client, store: store, token: strings.TrimSpace(token),
		wmdbBase: defaultWMDBBase, tmdbBase: defaultTMDBBase,
		lookup: outbound.NewLimiter(defaultIMDbLookupInterval),
	}
	for _, option := range options {
		option(provider)
	}
	return provider
}

func (provider *TMDBProvider) SyncBackdrops(ctx context.Context, doubanID string) error {
	// 配置缺失和 ID 非法都不会因为重试而改变，直接判死，别占着重试预算。
	if provider.token == "" {
		return workqueue.Terminal(fmt.Errorf("TMDB_API_TOKEN is not configured"))
	}
	if !validDoubanID(doubanID) {
		return workqueue.Terminal(fmt.Errorf("invalid Douban ID %q", doubanID))
	}
	_, err, _ := provider.group.Do(doubanID, func() (any, error) {
		return nil, provider.sync(ctx, doubanID)
	})
	return err
}

func (provider *TMDBProvider) sync(ctx context.Context, doubanID string) error {
	movie, err := provider.store.FindByDoubanID(ctx, doubanID)
	if err != nil {
		return fmt.Errorf("find movie for TMDB sync: %w", err)
	}
	if movie == nil {
		return workqueue.Terminal(fmt.Errorf("movie not found: %s", doubanID))
	}
	if movie.IMDbID == "" {
		movie.IMDbID, err = provider.fetchIMDbID(ctx, doubanID)
		if err != nil {
			return fmt.Errorf("fetch IMDb ID: %w", err)
		}
	}
	if movie.IMDbID == "" {
		// 上游返回了 200 但没有映射关系，等于这个条目在 wmdb 里没有 IMDb ID。
		return workqueue.Terminal(fmt.Errorf("IMDb ID not found for Douban ID %s", doubanID))
	}
	targetSeason := mediaidentity.TitleSeasonNumber(movie.Title, movie.OriginalTitle)
	tmdbID, mediaType, err := provider.findTMDBID(ctx, movie.IMDbID)
	if errors.Is(err, errTMDBResultNotFound) && targetSeason > 0 {
		tmdbID, mediaType, err = provider.searchTMDBTV(ctx, mediaidentity.TitleBase(movie.OriginalTitle, movie.Title))
	}
	if err != nil {
		if errors.Is(err, errTMDBResultNotFound) {
			return workqueue.Terminal(err)
		}
		return err
	}
	images, imagesErr := provider.fetchImages(ctx, tmdbID, mediaType)
	details, detailsErr := provider.fetchDetails(ctx, tmdbID, mediaType)
	if imagesErr != nil && detailsErr != nil {
		return fmt.Errorf("fetch TMDB images: %v; fetch details: %v", imagesErr, detailsErr)
	}
	applyTMDBData(movie, images, details)
	if err := provider.store.Upsert(ctx, *movie); err != nil {
		return fmt.Errorf("save TMDB movie data: %w", err)
	}
	payload := struct {
		DoubanID  string `json:"douban_id"`
		IMDbID    string `json:"imdb_id"`
		TMDBID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
		Images    any    `json:"images"`
		Details   any    `json:"details"`
	}{movie.DoubanID, movie.IMDbID, tmdbID, mediaType, images, details}
	canonical := mediaidentity.Media{
		MediaType: mediaType, DoubanID: movie.DoubanID, Title: movie.Title,
		OriginalTitle: movie.OriginalTitle, Year: movie.Year, Poster: movie.Poster,
		Backdrops: movie.Backdrops, Summary: movie.Summary, Genres: movie.Genres,
		Countries: movie.Countries, Directors: movie.Directors, Actors: movie.Actors,
		Duration: movie.Duration, RatingDouban: movie.Rating, MetadataStatus: "partial",
		SeriesStatus: movie.SeriesStatus,
	}
	if details != nil {
		canonical.RatingTMDB = details.VoteAverage
		canonical.VoteCountTMDB = details.VoteCount
	}
	externalType := mediaType
	if mediaType == "tv" {
		if targetSeason > 0 {
			externalType = fmt.Sprintf("tv_season_%d", targetSeason)
		}
	}
	mediaID, err := syncCanonicalMedia(ctx, provider.canonical, canonical, "tmdb", payload,
		mediaidentity.ExternalID{Provider: "douban", ExternalType: mediaType, ExternalID: movie.DoubanID, IsPrimary: true},
		mediaidentity.ExternalID{Provider: "imdb", ExternalType: externalType, ExternalID: movie.IMDbID, IsPrimary: true},
		mediaidentity.ExternalID{Provider: "tmdb", ExternalType: externalType, ExternalID: fmt.Sprintf("%d", tmdbID), IsPrimary: true})
	if err != nil {
		// 旧 catalog 已经更新成功；规范持久化只是附加副作用。
		// migration 0013 不可用时，不能把一次成功的 TMDB 刷新变成用户可见失败。
		return nil
	}
	// 电视剧还需要把季集元数据同步到 media_units。
	if mediaType == "tv" && mediaID > 0 {
		provider.syncTVSeasons(ctx, mediaID, tmdbID, targetSeason, details)
	}
	return nil
}

func (provider *TMDBProvider) fetchIMDbID(ctx context.Context, doubanID string) (string, error) {
	if err := provider.lookup.Wait(ctx); err != nil {
		return "", err
	}
	endpoint := provider.wmdbBase + "/movie/api?id=" + url.QueryEscape(doubanID)
	var response struct {
		IMDbID string `json:"imdbId"`
	}
	if err := provider.getJSON(ctx, endpoint, false, &response); err != nil {
		// 限流是整个进程共享的状态：一个任务撞上 429，其余任务也必须一起等，
		// 否则退避只是把同一波请求换个 worker 再打一遍。
		if retryAfter, throttled := workqueue.RetryAfter(err); throttled {
			if retryAfter <= 0 {
				retryAfter = 30 * time.Second
			}
			provider.lookup.Pause(retryAfter)
		}
		if status, ok := upstreamStatus(err); ok && status == http.StatusNotFound {
			// wmdb 没收录这个条目，重试四次也不会凭空出现。
			return "", workqueue.Terminal(err)
		}
		return "", err
	}
	return strings.TrimSpace(response.IMDbID), nil
}

type tmdbFindResponse struct {
	MovieResults []struct {
		ID int `json:"id"`
	} `json:"movie_results"`
	TVResults []struct {
		ID int `json:"id"`
	} `json:"tv_results"`
}

func (provider *TMDBProvider) findTMDBID(ctx context.Context, imdbID string) (int, string, error) {
	endpoint := fmt.Sprintf("%s/3/find/%s?external_source=imdb_id&language=zh-CN", provider.tmdbBase, url.PathEscape(imdbID))
	var response tmdbFindResponse
	if err := provider.getJSON(ctx, endpoint, true, &response); err != nil {
		return 0, "", fmt.Errorf("find TMDB ID: %w", err)
	}
	if len(response.MovieResults) > 0 {
		return response.MovieResults[0].ID, "movie", nil
	}
	if len(response.TVResults) > 0 {
		return response.TVResults[0].ID, "tv", nil
	}
	return 0, "", fmt.Errorf("%w for IMDb ID %s", errTMDBResultNotFound, imdbID)
}

type tmdbTVSearchResponse struct {
	Results []struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		OriginalName string `json:"original_name"`
	} `json:"results"`
}

func (provider *TMDBProvider) searchTMDBTV(ctx context.Context, title string) (int, string, error) {
	if title == "" {
		return 0, "", fmt.Errorf("%w for empty TV title", errTMDBResultNotFound)
	}
	endpoint := fmt.Sprintf("%s/3/search/tv?query=%s&language=zh-CN", provider.tmdbBase, url.QueryEscape(title))
	var response tmdbTVSearchResponse
	if err := provider.getJSON(ctx, endpoint, true, &response); err != nil {
		return 0, "", fmt.Errorf("search TMDB TV: %w", err)
	}
	titleKey := mediaidentity.NormalizeTitle(title)
	for _, result := range response.Results {
		if result.ID > 0 && (mediaidentity.NormalizeTitle(result.Name) == titleKey || mediaidentity.NormalizeTitle(result.OriginalName) == titleKey) {
			return result.ID, "tv", nil
		}
	}
	return 0, "", fmt.Errorf("%w for TV title %s", errTMDBResultNotFound, title)
}

type tmdbImagesResponse struct {
	Backdrops []struct {
		FilePath string `json:"file_path"`
	} `json:"backdrops"`
	Posters []struct {
		FilePath string `json:"file_path"`
	} `json:"posters"`
}

func (provider *TMDBProvider) fetchImages(ctx context.Context, tmdbID int, mediaType string) (*tmdbImagesResponse, error) {
	endpoint := fmt.Sprintf("%s/3/%s/%d/images?include_image_language=zh,en,null", provider.tmdbBase, mediaType, tmdbID)
	var response tmdbImagesResponse
	if err := provider.getJSON(ctx, endpoint, true, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

type tmdbDetailsResponse struct {
	OriginalTitle   string  `json:"original_title"`
	Overview        string  `json:"overview"`
	ReleaseDate     string  `json:"release_date"`
	FirstAirDate    string  `json:"first_air_date"`
	Runtime         int     `json:"runtime"`
	EpisodeRunTime  []int   `json:"episode_run_time"`
	VoteAverage     float64 `json:"vote_average"`
	VoteCount       int     `json:"vote_count"`
	NumberOfSeasons int     `json:"number_of_seasons"`
	// Status 是 TMDB 的连载状态：Returning Series / Ended / Canceled / In Production / Planned。
	Status       string `json:"status"`
	InProduction bool   `json:"in_production"`
	// NextEpisodeToAir 是"下一集何时更新"的最省成本来源：
	// 一次 /3/tv/{id} 详情请求就能拿到，不必逐季拉 season 接口。
	// 已完结剧集该字段为 null。
	NextEpisodeToAir *tmdbEpisodeStub `json:"next_episode_to_air"`
	LastEpisodeToAir *tmdbEpisodeStub `json:"last_episode_to_air"`
	Genres           []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Seasons []struct {
		SeasonNumber int    `json:"season_number"`
		EpisodeCount int    `json:"episode_count"`
		Name         string `json:"name"`
		AirDate      string `json:"air_date"`
	} `json:"seasons"`
}

type tmdbEpisodeStub struct {
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	AirDate       string `json:"air_date"`
	Runtime       int    `json:"runtime"`
}

type tmdbSeasonResponse struct {
	SeasonNumber int `json:"season_number"`
	Episodes     []struct {
		EpisodeNumber int    `json:"episode_number"`
		Name          string `json:"name"`
		AirDate       string `json:"air_date"`
		Runtime       int    `json:"runtime"`
	} `json:"episodes"`
}

func (provider *TMDBProvider) fetchDetails(ctx context.Context, tmdbID int, mediaType string) (*tmdbDetailsResponse, error) {
	endpoint := fmt.Sprintf("%s/3/%s/%d?language=zh-CN", provider.tmdbBase, mediaType, tmdbID)
	var response tmdbDetailsResponse
	if err := provider.getJSON(ctx, endpoint, true, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (provider *TMDBProvider) fetchTVSeason(ctx context.Context, tmdbID, seasonNumber int) (*tmdbSeasonResponse, error) {
	endpoint := fmt.Sprintf("%s/3/tv/%d/season/%d?language=zh-CN", provider.tmdbBase, tmdbID, seasonNumber)
	var response tmdbSeasonResponse
	if err := provider.getJSON(ctx, endpoint, true, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// syncTVSeasons 从 TMDB 获取季集数据并写入 media_units。
func (provider *TMDBProvider) syncTVSeasons(ctx context.Context, mediaID, tmdbID, targetSeason int, details *tmdbDetailsResponse) {
	if provider.units == nil || details == nil {
		return
	}
	// 详情响应自带的 next/last episode 先落库：即使后面逐季拉取失败或被超时打断，
	// "下一集何时更新"这一条追剧最需要的信息也已经写入。
	provider.syncEpisodeStubs(ctx, mediaID, targetSeason, details)
	if len(details.Seasons) == 0 {
		return
	}
	for _, season := range details.Seasons {
		if season.SeasonNumber <= 0 { // skip specials (season 0)
			continue
		}
		if targetSeason > 0 && season.SeasonNumber != targetSeason {
			continue
		}
		seasonData, err := provider.fetchTVSeason(ctx, tmdbID, season.SeasonNumber)
		if err != nil {
			continue // non-fatal: skip this season
		}
		for _, ep := range seasonData.Episodes {
			unit := mediaidentity.MediaUnit{
				MediaID:        mediaID,
				UnitType:       "episode",
				SeasonNumber:   season.SeasonNumber,
				EpisodeNumber:  ep.EpisodeNumber,
				EpisodeKey:     fmt.Sprintf("S%02dE%02d", season.SeasonNumber, ep.EpisodeNumber),
				Title:          ep.Name,
				RuntimeMinutes: ep.Runtime,
			}
			if ep.AirDate != "" {
				if parsed, parseErr := parseDate(ep.AirDate); parseErr == nil {
					unit.AirDate = parsed
				}
			}
			_, _ = provider.units.EnsureMediaUnit(ctx, unit)
		}
	}
}

// syncEpisodeStubs 把详情响应里的 next_episode_to_air / last_episode_to_air 写入 media_units。
func (provider *TMDBProvider) syncEpisodeStubs(ctx context.Context, mediaID, targetSeason int, details *tmdbDetailsResponse) {
	for _, stub := range []*tmdbEpisodeStub{details.NextEpisodeToAir, details.LastEpisodeToAir} {
		if stub == nil || stub.SeasonNumber <= 0 || stub.EpisodeNumber <= 0 {
			continue
		}
		if targetSeason > 0 && stub.SeasonNumber != targetSeason {
			continue
		}
		unit := mediaidentity.MediaUnit{
			MediaID:        mediaID,
			UnitType:       "episode",
			SeasonNumber:   stub.SeasonNumber,
			EpisodeNumber:  stub.EpisodeNumber,
			EpisodeKey:     fmt.Sprintf("S%02dE%02d", stub.SeasonNumber, stub.EpisodeNumber),
			Title:          stub.Name,
			RuntimeMinutes: stub.Runtime,
		}
		if stub.AirDate != "" {
			if parsed, err := parseDate(stub.AirDate); err == nil {
				unit.AirDate = parsed
			}
		}
		_, _ = provider.units.EnsureMediaUnit(ctx, unit)
	}
}

func (provider *TMDBProvider) getJSON(ctx context.Context, endpoint string, authorize bool, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if authorize {
		request.Header.Set("Authorization", "Bearer "+provider.token)
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return classifyUpstreamStatus("upstream", response)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode upstream response: %w", err)
	}
	return nil
}

func applyTMDBData(movie *Movie, images *tmdbImagesResponse, details *tmdbDetailsResponse) {
	if images != nil {
		backdrops := make([]string, 0, len(images.Backdrops))
		for _, backdrop := range images.Backdrops {
			if backdrop.FilePath != "" {
				backdrops = append(backdrops, "https://image.tmdb.org/t/p/w1280"+backdrop.FilePath)
			}
		}
		if len(backdrops) > 0 {
			movie.Backdrops = strings.Join(backdrops, ",")
		}
		if strings.TrimSpace(movie.Poster) == "" && len(images.Posters) > 0 && images.Posters[0].FilePath != "" {
			// 已有旧豆瓣海报时继续保留，它是稳定的 SEO/封面身份。
			// TMDB 主要补充剧照，只在记录没有旧封面时作为海报后备。
			movie.Poster = "https://image.tmdb.org/t/p/w500" + images.Posters[0].FilePath
		}
	}
	if details == nil {
		return
	}
	if movie.OriginalTitle == "" {
		movie.OriginalTitle = details.OriginalTitle
	}
	if movie.Summary == "" {
		movie.Summary = details.Overview
	}
	if movie.Year == "" {
		date := details.ReleaseDate
		if date == "" {
			date = details.FirstAirDate
		}
		if len(date) >= 4 {
			movie.Year = date[:4]
		}
	}
	if movie.Duration == "" {
		switch {
		case details.Runtime > 0:
			movie.Duration = fmt.Sprintf("%d分钟", details.Runtime)
		case len(details.EpisodeRunTime) > 0:
			movie.Duration = fmt.Sprintf("%d分钟", details.EpisodeRunTime[0])
		}
	}
	if movie.Genres == "" && len(details.Genres) > 0 {
		genres := make([]string, 0, len(details.Genres))
		for _, genre := range details.Genres {
			genres = append(genres, genre.Name)
		}
		movie.Genres = strings.Join(genres, "/")
	}
	// 连载状态始终以最新一次 TMDB 结果为准，不做"仅在为空时填充"。
	// 一部剧从在播变完结是必须被追上的状态迁移，保留旧值会让详情页一直显示"更新中"。
	if status := strings.TrimSpace(details.Status); status != "" {
		movie.SeriesStatus = status
	}
}

func parseDate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", value)
}
