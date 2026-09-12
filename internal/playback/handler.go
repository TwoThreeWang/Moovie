package playback

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaview"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/ratelimit"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
)

// PlayViewInfo 是播放页统一 ViewModel。它把可用的规范媒体资料与原始 vod_item 详情合并，
// 模板不需要再实现两套数据源的条件判断。
type PlayViewInfo = mediaview.Info

// linkedMediaResolver 是可选能力：存储实现如果同时支持按 ID 和按资源关联查媒体，
// 播放页就能拿到更准确的规范资料。
type linkedMediaResolver interface {
	mediaidentity.Resolver
	FindByID(ctx context.Context, id int) (mediaidentity.Media, error)
	FindResourceLink(ctx context.Context, sourceKey, vodID string) (mediaidentity.ResourceLink, error)
	FindLinkedResource(ctx context.Context, mediaID int) (mediaidentity.ResourceLink, error)
}

// EpisodeSourceView 是同一集的预处理备选来源，可直接由模板渲染为可点击条目。
type EpisodeSourceView struct {
	SourceKey    string
	VodID        string
	LineLabel    string
	SourceLabel  string // "sourceKey · lineLabel"
	VersionLabel string // 电影的清晰度/语言版本：HD中字、TC国语、正片……剧集为空
	QualityLabel string // 稳定/一般/较差/未测
	QualityClass string // stable/normal/unstable/unknown
	SpeedLabel   string // "0.8秒" or ""
	SpeedClass   string // fast/normal/slow or ""
	PlayLink     string // /play/sourceKey/vodID?ep=...&douban_id=...
	IsCurrent    bool
}

// buildPlayView 使用全站记录级资料规则，规范记录存在时不混用资源站字段。
// 播放地址等资源信息始终来自资源站。
func buildPlayView(media *mediaidentity.Media, detail *search.VodItem) PlayViewInfo {
	var canonical *mediaview.Info
	if media != nil {
		canonical = &mediaview.Info{Title: media.Title, Poster: media.Poster, Rating: media.RatingDouban, Year: media.Year,
			Genres: splitTrimmed(media.Genres), Countries: splitTrimmed(media.Countries), Directors: parsePeopleJSON(media.Directors, 3), Actors: parsePeopleJSON(media.Actors, 5), Summary: media.Summary}
	}
	var resource mediaview.Info
	if detail != nil {
		resource = mediaview.Info{Title: detail.VodName, Poster: detail.VodPic, Year: detail.VodYear, Genres: detail.GetGenres(), Countries: splitTrimmed(detail.VodArea),
			Directors: strings.Join(detail.GetDirectors(), " / "), Actors: strings.Join(detail.GetActors(), " / "), Summary: stripHTML(detail.VodContent)}
	}
	return mediaview.Choose(canonical, resource)
}

var reHTMLTag = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = reHTMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// resolveDisplayMedia 找出这条资源对应的规范媒体：先按 media_id，再按资源关联，最后按豆瓣 ID。
func (handler *Handler) resolveDisplayMedia(ctx context.Context, detail *search.VodItem, doubanID string) *mediaidentity.Media {
	if handler.media == nil || detail == nil {
		return nil
	}
	if resolver, ok := handler.media.(linkedMediaResolver); ok {
		mediaID := detail.MediaID
		if mediaID <= 0 {
			if link, err := resolver.FindResourceLink(ctx, detail.SourceKey, detail.VodId); err == nil {
				mediaID = link.MediaID
			}
		}
		if mediaID > 0 {
			if media, err := resolver.FindByID(ctx, mediaID); err == nil && media.ID > 0 {
				return &media
			}
		}
	}
	if doubanID != "" {
		if media, err := handler.media.FindByDoubanID(ctx, doubanID); err == nil && media.ID > 0 {
			return &media
		}
	}
	return nil
}

// buildEpisodeSources 把候选列表转成模板可直接渲染的换源条目。
func buildEpisodeSources(candidates []SourceCandidate, currentSourceKey, currentVodID, currentPlayURL, episode, doubanID string) []EpisodeSourceView {
	sources := make([]EpisodeSourceView, 0, len(candidates))
	for _, c := range candidates {
		isCurrent := c.SourceKey == currentSourceKey && c.VodID == currentVodID && c.PlayURL == currentPlayURL
		label := c.SourceKey
		if c.LineLabel != "" {
			label += " · " + c.LineLabel
		}
		qualityLabel, qualityClass := episodeQualityInfo(c.Health)
		speedLabel, speedClass := episodeSpeedInfo(c.Health)
		ep := c.EpisodeLabel
		if ep == "" {
			ep = episode
		}
		playLink := fmt.Sprintf("/play/%s/%s?ep=%s", c.SourceKey, c.VodID, url.QueryEscape(ep))
		if c.LineLabel != "" {
			playLink += "&source=" + url.QueryEscape(c.LineLabel)
		}
		if doubanID != "" {
			playLink += "&douban_id=" + url.QueryEscape(doubanID)
		}
		version := c.Quality
		if c.Part != "" {
			version = c.Part + "段"
		}
		sources = append(sources, EpisodeSourceView{
			SourceKey: c.SourceKey, VodID: c.VodID, LineLabel: c.LineLabel, SourceLabel: label,
			VersionLabel: version, QualityLabel: qualityLabel, QualityClass: qualityClass,
			SpeedLabel: speedLabel, SpeedClass: speedClass,
			PlayLink: playLink, IsCurrent: isCurrent,
		})
	}
	return sources
}

// episodeQualityInfo 把质量分转成中文标签：≥0.75 稳定，≥0.5 一般，否则较差；没样本是未测。
// 四个标签一律两个字，中文等宽，线路网格里的标签和右侧速度才能自然对齐。
func episodeQualityInfo(health PlaybackHealth) (string, string) {
	if health.Total() == 0 {
		return "未测", "unknown"
	}
	score := health.Score()
	if score >= 0.75 {
		return "稳定", "stable"
	}
	if score >= 0.5 {
		return "一般", "normal"
	}
	return "较差", "unstable"
}

// episodeSpeedInfo 把平均加载耗时转成中文标签：1 秒内快，3 秒内正常，更慢就是慢。
func episodeSpeedInfo(health PlaybackHealth) (string, string) {
	if health.AvgLoadMs <= 0 || health.Total() == 0 {
		return "", ""
	}
	seconds := float64(health.AvgLoadMs) / 1000
	label := fmt.Sprintf("%.1f秒", seconds)
	if seconds < 1.0 {
		return label, "fast"
	}
	if seconds < 3.0 {
		return label, "normal"
	}
	return label, "slow"
}

// parsePeopleJSON 把 [{"name":...}] 形式的演职员 JSON 转成「甲 / 乙 / 丙」，解析失败就原样返回。
func parsePeopleJSON(value string, limit int) string {
	var people []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(value), &people) != nil {
		return value
	}
	names := make([]string, 0, limit)
	for _, p := range people {
		if len(names) >= limit {
			break
		}
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return strings.Join(names, " / ")
}

// splitTrimmed 按逗号切分并去空白。
func splitTrimmed(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// Handler 提供播放相关的所有页面和接口。带 With 前缀的可选依赖为 nil 时对应功能自动降级。
type Handler struct {
	config         config.Config
	catalog        Catalog
	details        *DetailService
	popular        PopularProvider
	titleFinder    MovieTitleFinder
	speeds         SpeedStore
	copyright      CopyrightChecker
	userMovies     UserMovieStore
	media          mediaidentity.Resolver
	episodes       mediaidentity.EpisodeReader
	events         mediaidentity.PlaybackEventWriter
	airSchedule    AirScheduleReader
	eventLimiter   *ratelimit.PerIP
	adFingerprints AdFingerprintStore
	adVoteLimiter  *ratelimit.PerIP
}

// AirScheduleReader 提供某部作品尚未播出的剧集，用于播放页展示更新时间。
// 为 nil 时播放页安全降级为不展示该区块。
type AirScheduleReader interface {
	ListUpcomingUnits(ctx context.Context, mediaID, seasonNumber int, from time.Time, limit int) ([]mediaidentity.MediaUnit, error)
}

// HandlerOption 用于注入可选依赖。
type HandlerOption func(*Handler)

// WithSpeedStore 注入线路测速存储。
func WithSpeedStore(store SpeedStore) HandlerOption {
	return func(handler *Handler) { handler.speeds = store }
}

// WithCopyrightChecker 注入版权屏蔽检查。
func WithCopyrightChecker(checker CopyrightChecker) HandlerOption {
	return func(handler *Handler) { handler.copyright = checker }
}

// WithUserMovieStore 注入片单存储。
func WithUserMovieStore(store UserMovieStore) HandlerOption {
	return func(handler *Handler) { handler.userMovies = store }
}

// WithMediaResolver 注入媒体识别，把资源对应到具体影片。
func WithMediaResolver(resolver mediaidentity.Resolver) HandlerOption {
	return func(handler *Handler) { handler.media = resolver }
}

// WithEpisodeReader 注入分集信息读取。
func WithEpisodeReader(reader mediaidentity.EpisodeReader) HandlerOption {
	return func(handler *Handler) { handler.episodes = reader }
}

// WithPlaybackEventWriter 注入播放埋点写入。
func WithPlaybackEventWriter(writer mediaidentity.PlaybackEventWriter) HandlerOption {
	return func(handler *Handler) { handler.events = writer }
}

// WithAirScheduleReader 注入播出时间表读取。
func WithAirScheduleReader(reader AirScheduleReader) HandlerOption {
	return func(handler *Handler) { handler.airSchedule = reader }
}

// WithAdFingerprintStore 注入广告指纹存储。
func WithAdFingerprintStore(store AdFingerprintStore) HandlerOption {
	return func(handler *Handler) {
		handler.adFingerprints = store
		handler.adVoteLimiter = ratelimit.NewPerIP(30, time.Minute)
	}
}

// NewHandler 创建播放处理器，播放事件上报默认限流每 IP 每分钟 120 次。
func NewHandler(cfg config.Config, catalog Catalog, details *DetailService, popular PopularProvider, titleFinder MovieTitleFinder, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, catalog: catalog, details: details, popular: popular, titleFinder: titleFinder,
		eventLimiter: ratelimit.NewPerIP(120, time.Minute)}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Register 注册路由：/play 和 /watch 是两个播放页入口（前者按资源，后者按豆瓣 ID），
// /api/v2/* 是播放候选和质量上报接口，/api/vod 和 /api/tvbox.json 供 TVBox 客户端使用。
func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/player", handler.player)
	router.GET("/iptv", handler.iptv)
	router.GET("/tvbox", handler.tvbox)
	router.GET("/api/tvbox.json", handler.tvboxConfig)
	router.GET("/api/vod", handler.tvboxVOD)
	router.GET("/play/:source_key/:vod_id", auth.Optional(handler.config.AppSecret), handler.play)
	router.GET("/watch/:douban_id", auth.Optional(handler.config.AppSecret), handler.watch)
	router.GET("/api/htmx/watch-actions", auth.Optional(handler.config.AppSecret), handler.watchActionsHTMX)
	router.GET("/api/htmx/watch-schedule", handler.watchScheduleHTMX)
	router.GET("/api/watch/resolve", handler.resolveWatchURL)
	router.GET("/api/v2/media/:id/resources", handler.resources)
	router.GET("/api/v2/media-units/:unit_id/playback-candidates", handler.playbackCandidatesV2)
	router.POST("/api/v2/playback/events", handler.playbackEventV2)
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.POST("/api/ad-fingerprints/match", require, handler.adFingerprintMatch)
	router.POST("/api/ad-fingerprints/vote", require, handler.adFingerprintVote)
}

// resources 返回某一集的全部播放源（按质量排序）。
func (handler *Handler) resources(c *gin.Context) {
	mediaID, err := strconv.Atoi(c.Param("id"))
	if err != nil || mediaID <= 0 {
		apiError(c, http.StatusBadRequest, "media_id 参数错误")
		return
	}
	season, _ := strconv.Atoi(c.DefaultQuery("season", "1"))
	if season < 1 {
		season = 1
	}
	episodeKey := strings.TrimSpace(c.Query("episode_key"))
	if episodeKey == "" {
		_, episodeKey = mediaidentity.NormalizeEpisodeLabel(c.Query("ep"))
	}
	if episodeKey == "" {
		apiError(c, http.StatusBadRequest, "episode_key 参数错误")
		return
	}
	if handler.episodes == nil {
		c.JSON(http.StatusOK, gin.H{"media_id": mediaID, "season_number": season, "episode_key": episodeKey, "resources": []gin.H{}})
		return
	}
	candidates, err := handler.episodes.ListResourceCandidates(c.Request.Context(), mediaID, season, episodeKey)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "获取播放源失败")
		return
	}
	sources := make([]SourceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		sources = append(sources, sourceCandidate(candidate))
	}
	ranked := filterSameEpisode(sources, season, episodeKey)
	ranked = RankSameEpisode(ranked, season, episodeKey)
	list := make([]gin.H, 0, len(ranked))
	for _, source := range ranked {
		list = append(list, gin.H{"source_key": source.SourceKey, "vod_id": source.VodID, "media_id": source.MediaID,
			"season_number": source.SeasonNumber, "episode_key": source.EpisodeKey, "play_url": source.PlayURL,
			"avg_load_ms": source.Health.AvgLoadMs, "success_count": source.Health.SuccessCount, "failure_count": source.Health.FailureCount,
			"score": source.Health.Score()})
	}
	c.JSON(http.StatusOK, gin.H{"media_id": mediaID, "season_number": season, "episode_key": episodeKey,
		"resources": list})
}

// playbackCandidatesV2 按季集 ID 返回播放候选，播放器用它做自动换源。
func (handler *Handler) playbackCandidatesV2(c *gin.Context) {
	unitID, err := strconv.Atoi(c.Param("unit_id"))
	if err != nil || unitID <= 0 {
		apiError(c, http.StatusBadRequest, "media_unit_id 参数错误")
		return
	}
	reader, ok := handler.episodes.(mediaidentity.UnitEpisodeReader)
	if !ok {
		apiError(c, http.StatusServiceUnavailable, "播放候选服务暂时不可用")
		return
	}
	candidates, err := reader.ListUnitResourceCandidates(c.Request.Context(), unitID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "获取播放候选失败")
		return
	}
	sources := make([]SourceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.MediaUnitID == unitID {
			sources = append(sources, sourceCandidate(candidate))
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Score() > sources[j].Score() })
	items := make([]gin.H, 0, len(sources))
	mediaID := 0
	for _, source := range sources {
		mediaID = source.MediaID
		items = append(items, gin.H{
			"candidate_key": source.CandidateKey, "playback_version": source.PlaybackVersion, "part": source.Part,
			"line_key": source.LineKey, "line_label": source.LineLabel,
			"source_key": source.SourceKey, "vod_id": source.VodID, "play_url": source.PlayURL,
			"episode_key": source.EpisodeKey, "episode_label": source.EpisodeLabel,
			"score": source.Score(), "quality_label": playbackQualityLabel(source.Health),
			"mapping_confidence": source.MappingConfidence,
		})
	}
	c.JSON(http.StatusOK, gin.H{"media_id": mediaID, "unit_id": unitID, "resume_position": 0,
		// player.js 依据该字段决定是否自动换源，自动换源已固定启用。
		"auto_failover_enabled": true, "candidates": items})
}

// playbackEventRequest 只接收一次起播的最终结果；候选/线路数据库 ID 已移除。
type playbackEventRequest struct {
	AttemptID       string `json:"attempt_id"`
	EventType       string `json:"event_type"`
	MediaUnitID     int    `json:"media_unit_id"`
	SourceKey       string `json:"source_key"`
	VodID           string `json:"vod_id"`
	PlaybackVersion string `json:"playback_version"`
	ElapsedMs       int    `json:"elapsed_ms"`
}

func (handler *Handler) playbackEventV2(c *gin.Context) {
	if !handler.eventLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, "播放上报过于频繁")
		return
	}
	if handler.events == nil {
		apiError(c, http.StatusServiceUnavailable, "播放统计暂不可用")
		return
	}
	var r playbackEventRequest
	if c.ShouldBindJSON(&r) != nil {
		apiError(c, http.StatusBadRequest, "播放结果参数错误")
		return
	}
	accepted, err := handler.events.RecordPlaybackEvent(c.Request.Context(), mediaidentity.PlaybackAttemptEvent{
		AttemptID: r.AttemptID, EventType: r.EventType, MediaUnitID: r.MediaUnitID, SourceKey: r.SourceKey, VodID: r.VodID, PlaybackVersion: r.PlaybackVersion, ElapsedMs: r.ElapsedMs})
	if err != nil {
		if errors.Is(err, mediaidentity.ErrInvalidPlaybackEvent) {
			apiError(c, http.StatusBadRequest, "播放结果参数错误")
		} else {
			requestmeta.Logger(c.Request.Context()).Error("save playback result", "error", err)
			apiError(c, http.StatusInternalServerError, "播放结果保存失败")
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"accepted": accepted})
}

// sourceCandidate 把存储层候选转成本包的排序用结构。
func sourceCandidate(candidate mediaidentity.ResourceCandidate) SourceCandidate {
	return SourceCandidate{CandidateKey: candidate.CandidateKey, PlaybackVersion: candidate.PlaybackVersion, Part: candidate.Part,
		LineKey: candidate.LineKey, LineLabel: candidate.LineLabel,
		SourceKey: candidate.SourceKey, VodID: candidate.VodID, MediaID: candidate.MediaID, MediaUnitID: candidate.MediaUnitID,
		SeasonNumber: candidate.SeasonNumber, EpisodeKey: candidate.EpisodeKey, EpisodeLabel: candidate.EpisodeLabel,
		Quality: candidate.Quality, PlayURL: candidate.PlayURL,
		MappingConfidence: candidate.MappingConfidence,
		Health:            PlaybackHealth{SuccessCount: candidate.SuccessCount, FailureCount: candidate.FailureCount, AvgLoadMs: candidate.AvgLoadMs}}
}

// playbackQualityLabel 质量分对应的中文标签（接口版）。
func playbackQualityLabel(health PlaybackHealth) string {
	label, _ := episodeQualityInfo(health)
	return label
}

// airScheduleUpcomingLimit 限制播放页一次展示的未播出集数。
const airScheduleUpcomingLimit = 8

// airScheduleView 组装"下一集何时更新"区块。
// 与详情页一致，任何一步缺数据都返回零值视图交给模板整块跳过；
// 播放页的主职责是播放，更新时间查询失败不应干扰它。
func (handler *Handler) airScheduleView(ctx context.Context, media *mediaidentity.Media) mediaidentity.AirScheduleView {
	if handler.airSchedule == nil || media == nil || media.ID <= 0 {
		return mediaidentity.AirScheduleView{}
	}
	if mediaidentity.SeriesEnded(media.SeriesStatus) {
		return mediaidentity.AirScheduleView{}
	}
	location := mediaidentity.AiringLocation(handler.config.Database.TimeZone)
	now := time.Now()
	seasonNumber := mediaidentity.TitleSeasonNumber(media.Title, media.OriginalTitle)
	units, err := handler.airSchedule.ListUpcomingUnits(ctx, media.ID, seasonNumber,
		mediaidentity.AiringDay(now, location), airScheduleUpcomingLimit)
	if err != nil {
		requestmeta.Logger(ctx).Warn("load air schedule failed", "media_id", media.ID, "error", err)
		return mediaidentity.AirScheduleView{}
	}
	return mediaidentity.BuildAirScheduleView(media.SeriesStatus, units, now, location)
}

// ---------------------------------------------------------------------------
// /watch/:douban_id：以规范媒体内容为入口的播放页。
// ---------------------------------------------------------------------------

// watchActionsHTMX 返回播放页的"看过"按钮（htmx 延迟加载，不阻塞播放器启动）。
func (handler *Handler) watchActionsHTMX(c *gin.Context) {
	doubanID := c.Query("douban_id")
	userID := auth.UserID(c)
	isWatched := false
	if userID > 0 && handler.userMovies != nil {
		isWatched, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, doubanID, "watched")
	}
	c.HTML(http.StatusOK, "partials/play_watched_button.html", gin.H{
		"DoubanID": doubanID, "Title": c.Query("title"), "Poster": c.Query("poster"),
		"Year": c.Query("year"), "IsWatched": isWatched, "LoggedIn": userID > 0,
		"Redirect": c.Query("redirect"),
	})
}

// watchScheduleHTMX 返回播放页的播出日程（htmx 延迟加载）。
func (handler *Handler) watchScheduleHTMX(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" || handler.media == nil {
		c.String(http.StatusOK, "")
		return
	}
	canonical, err := handler.media.FindByDoubanID(c.Request.Context(), doubanID)
	if err != nil || canonical.ID == 0 {
		c.String(http.StatusOK, "")
		return
	}
	schedule := handler.airScheduleView(c.Request.Context(), &canonical)
	c.HTML(http.StatusOK, "partials/air_schedule.html", schedule)
}

// watchSearchKeyword 在 /watch 走不通需要回搜索页时，优先用影片标题做关键词。
// 延迟到错误路径才调用，不阻塞正常播放的主路径。
func (handler *Handler) watchSearchKeyword(ctx context.Context, doubanID string) string {
	if handler.titleFinder != nil {
		if title, _ := handler.titleFinder.FindTitleByDoubanID(ctx, doubanID); title != "" {
			return title
		}
	}
	return doubanID
}

// resolveWatchURL 返回指定媒体和剧集组合的最佳播放地址。
// watch 页 JavaScript 用它在不刷新整个页面的情况下切换剧集或来源。
func (handler *Handler) resolveWatchURL(c *gin.Context) {
	mediaID, _ := strconv.Atoi(c.Query("media_id"))
	epParam := c.Query("ep")
	seasonNumber, episodeKey := mediaidentity.NormalizeEpisodeLabel(epParam)
	if handler.episodes != nil {
		infos, err := handler.episodes.ListAllEpisodes(c.Request.Context(), mediaID)
		if err != nil {
			apiError(c, http.StatusServiceUnavailable, "资源读取失败")
			return
		}
		unitID, _ := strconv.Atoi(c.Query("unit"))
		seasonNumber, episodeKey = selectContent(infos, unitID, epParam)
	}
	doubanID := c.Query("douban_id")
	forceSource := c.Query("source_key")
	forceVodID := c.Query("vod_id")

	if mediaID <= 0 || episodeKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整"})
		return
	}
	if handler.episodes == nil {
		c.JSON(http.StatusOK, gin.H{"error": "无可用源"})
		return
	}

	raw, err := handler.episodes.ListResourceCandidates(c.Request.Context(), mediaID, seasonNumber, episodeKey)
	if err != nil || len(raw) == 0 {
		c.JSON(http.StatusOK, gin.H{"error": "未找到可用源"})
		return
	}
	var candidates []SourceCandidate
	for _, rc := range raw {
		candidates = append(candidates, sourceCandidate(rc))
	}
	ranked := filterSameEpisode(candidates, seasonNumber, episodeKey)
	ranked = RankSameEpisode(ranked, seasonNumber, episodeKey)
	if len(ranked) == 0 {
		c.JSON(http.StatusOK, gin.H{"error": "未找到可用源"})
		return
	}

	best, found := selectSource(ranked, forceSource, forceVodID, c.Query("source"), c.Query("ver"), c.Query("candidate"), c.Query("part"))
	if !found {
		c.JSON(http.StatusOK, gin.H{"error": "所选线路或版本已不可用"})
		return
	}

	qualityLabel, qualityClass := episodeQualityInfo(best.Health)
	speedLabel, speedClass := episodeSpeedInfo(best.Health)
	sourceLabel := best.SourceKey
	if best.LineLabel != "" {
		sourceLabel += " · " + best.LineLabel
	}

	// 为换源面板构建当前剧集的来源列表。
	epLabel := best.EpisodeLabel
	if epLabel == "" {
		epLabel = epParam
	}
	sourcesView := buildEpisodeSources(ranked, best.SourceKey, best.VodID, best.PlayURL, epLabel, doubanID)
	type sourceJSON struct {
		SourceKey    string `json:"source_key"`
		SourceLabel  string `json:"source_label"`
		QualityLabel string `json:"quality_label"`
		QualityClass string `json:"quality_class"`
		SpeedLabel   string `json:"speed_label,omitempty"`
		SpeedClass   string `json:"speed_class,omitempty"`
		PlayLink     string `json:"play_link"`
		IsCurrent    bool   `json:"is_current"`
	}
	var sourcesJSON []sourceJSON
	for _, sv := range sourcesView {
		sourcesJSON = append(sourcesJSON, sourceJSON{
			SourceKey:   sv.SourceKey,
			SourceLabel: sv.SourceLabel, QualityLabel: sv.QualityLabel, QualityClass: sv.QualityClass,
			SpeedLabel: sv.SpeedLabel, SpeedClass: sv.SpeedClass, PlayLink: sv.PlayLink, IsCurrent: sv.IsCurrent,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"play_url":      best.PlayURL,
		"source_key":    best.SourceKey,
		"vod_id":        best.VodID,
		"source_label":  sourceLabel,
		"quality_label": qualityLabel,
		"quality_class": qualityClass,
		"speed_label":   speedLabel,
		"speed_class":   speedClass,
		"episode_label": best.EpisodeLabel,
		"episode_key":   episodeKey,
		"candidate_key": best.CandidateKey, "playback_version": best.PlaybackVersion, "part": best.Part,
		"media_unit_id": best.MediaUnitID,
		"sources":       sourcesJSON,
	})
}

// adFingerprintMatch 批量匹配广告指纹。
func (handler *Handler) adFingerprintMatch(c *gin.Context) {
	if handler.adFingerprints == nil {
		apiError(c, http.StatusServiceUnavailable, "广告指纹服务暂时不可用")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
	if err != nil {
		apiError(c, http.StatusBadRequest, "请求读取失败")
		return
	}
	var request struct {
		Fingerprints []string `json:"fingerprints"`
	}
	if json.Unmarshal(body, &request) != nil {
		apiError(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(request.Fingerprints) > 20 {
		apiError(c, http.StatusBadRequest, "指纹数量超过上限")
		return
	}
	seen := make(map[string]bool, len(request.Fingerprints))
	var fingerprints [][]byte
	for _, hexStr := range request.Fingerprints {
		if seen[hexStr] {
			continue
		}
		seen[hexStr] = true
		fp, err := hex.DecodeString(hexStr)
		if err != nil || len(fp) != 32 {
			apiError(c, http.StatusBadRequest, "指纹格式错误")
			return
		}
		fingerprints = append(fingerprints, fp)
	}
	if len(fingerprints) == 0 {
		c.JSON(http.StatusOK, gin.H{"matches": gin.H{}})
		return
	}
	results, err := handler.adFingerprints.MatchFingerprints(c.Request.Context(), fingerprints)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "指纹匹配失败")
		return
	}
	matches := make(gin.H, len(results))
	for _, f := range results {
		matches[hex.EncodeToString(f.Fingerprint)] = gin.H{
			"status":        f.Status(),
			"confirm_count": f.ConfirmCount,
			"reject_count":  f.RejectCount,
		}
	}
	c.JSON(http.StatusOK, gin.H{"matches": matches})
}

// adFingerprintVote 提交广告指纹投票。
func (handler *Handler) adFingerprintVote(c *gin.Context) {
	if handler.adFingerprints == nil {
		apiError(c, http.StatusServiceUnavailable, "广告指纹服务暂时不可用")
		return
	}
	if handler.adVoteLimiter != nil && !handler.adVoteLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, "投票过于频繁")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024))
	if err != nil {
		apiError(c, http.StatusBadRequest, "请求读取失败")
		return
	}
	var request struct {
		Fingerprint string `json:"fingerprint"`
		Vote        string `json:"vote"`
	}
	if json.Unmarshal(body, &request) != nil {
		apiError(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	fp, err := hex.DecodeString(request.Fingerprint)
	if err != nil || len(fp) != 32 {
		apiError(c, http.StatusBadRequest, "指纹格式错误")
		return
	}
	var result *AdFingerprint
	switch request.Vote {
	case "confirm":
		result, err = handler.adFingerprints.VoteConfirm(c.Request.Context(), fp)
	case "reject":
		result, err = handler.adFingerprints.VoteReject(c.Request.Context(), fp)
	default:
		apiError(c, http.StatusBadRequest, "投票类型错误")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "投票失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"fingerprint":   request.Fingerprint,
		"status":        result.Status(),
		"confirm_count": result.ConfirmCount,
		"reject_count":  result.RejectCount,
	})
}

// apiError 统一的接口错误返回格式。
func apiError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"code": status, "message": message, "data": nil, "success": false})
}

// player 是独立的 M3U8 播放器工具页。
func (handler *Handler) player(c *gin.Context) {
	target := c.Query("url")
	if c.Query("embed") == "1" {
		c.HTML(http.StatusOK, "player_embed.html", gin.H{"URL": target})
		return
	}
	metadata := platformweb.Metadata{
		Title:       fmt.Sprintf("M3U8在线播放器 - HLS直播流测试工具 - 极简无广告 - %s", handler.config.SiteName),
		Description: fmt.Sprintf("%s 提供的免费 M3U8 在线播放工具。支持 HLS (.m3u8) 视频流测试，跨平台兼容，无需插件，高清流畅。适用于开发者测试和日常观影。", handler.config.SiteName),
		Keywords:    fmt.Sprintf("M3U8,在线播放,直播流测试,无广告,%s", handler.config.SiteName),
		Canonical:   platformweb.CanonicalURL(handler.config.SiteURL, "/player"),
	}
	c.HTML(http.StatusOK, "player.html", platformweb.NewData(c, handler.config, metadata, gin.H{"URL": target, "ContentClass": "full-width"}))
}

// iptv 是 IPTV 直播播放器页。
func (handler *Handler) iptv(c *gin.Context) {
	metadata := platformweb.Metadata{
		Title:       fmt.Sprintf("IPTV电视直播 - 全国卫视央视在线观看 - %s", handler.config.SiteName),
		Description: fmt.Sprintf("%s 提供的免费 IPTV 电视直播播放器。支持导入 M3U 直播源，观看央视、卫视等频道。", handler.config.SiteName),
		Keywords:    fmt.Sprintf("IPTV,电视直播,央视,卫视,在线观看,%s", handler.config.SiteName),
		Canonical:   platformweb.CanonicalURL(handler.config.SiteURL, "/iptv"),
	}
	c.HTML(http.StatusOK, "iptv.html", platformweb.NewData(c, handler.config, metadata, gin.H{"ContentClass": "full-width"}))
}

// tvbox 是 TVBox 配置说明页。
func (handler *Handler) tvbox(c *gin.Context) {
	metadata := platformweb.Metadata{Title: "TVBox 配置指南 - " + handler.config.SiteName, Canonical: platformweb.CanonicalURL(handler.config.SiteURL, "/tvbox")}
	c.HTML(http.StatusOK, "tvbox.html", platformweb.NewData(c, handler.config, metadata, gin.H{
		"TVBoxAPIURL": strings.TrimRight(handler.config.SiteURL, "/") + "/api/tvbox.json",
	}))
}

// tvboxConfig 返回 TVBox 客户端的订阅配置。
func (handler *Handler) tvboxConfig(c *gin.Context) {
	baseURL := requestBaseURL(c)
	c.JSON(http.StatusOK, gin.H{
		"sites": []gin.H{{
			"key": "moovie", "name": "Moovie 影牛", "type": 1,
			"api": baseURL + "/api/vod", "searchable": 1, "quickSearch": 1, "filterable": 0,
		}},
		"lives": []gin.H{}, "parses": []gin.H{}, "flags": []string{},
	})
}

// tvboxVOD 是 TVBox 的统一入口，按参数分发到详情/搜索/分类/热门。
func (handler *Handler) tvboxVOD(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("pg", "1"))
	if page < 1 {
		page = 1
	}
	if ids := c.Query("ids"); ids != "" {
		handler.tvboxDetail(c, ids)
		return
	}
	if keyword := c.Query("wd"); keyword != "" {
		handler.tvboxSearch(c, keyword, page)
		return
	}
	if c.Query("ac") == "detail" && c.Query("t") != "" {
		handler.tvboxCategory(c, c.Query("t"))
		return
	}
	handler.tvboxPopular(c, "movie", true)
}

// tvboxSearch 搜索并按每页 20 条分页返回。
func (handler *Handler) tvboxSearch(c *gin.Context, keyword string, page int) {
	items, err := handler.catalog.Search(c.Request.Context(), keyword)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("TVBox search failed", "error", err)
	}
	list := make([]gin.H, 0, len(items))
	canonical := map[int]*mediaidentity.Media{}
	if reader, ok := handler.catalog.(search.UnifiedMediaReader); ok {
		var ids []int
		for _, item := range items {
			if item.MediaID > 0 {
				ids = append(ids, item.MediaID)
			}
		}
		records, readErr := reader.ListUnifiedMedia(c.Request.Context(), ids)
		if readErr != nil {
			apiError(c, http.StatusServiceUnavailable, "作品资料暂时无法读取")
			return
		}
		for _, m := range records {
			canonical[m.MediaID] = &mediaidentity.Media{ID: m.MediaID, DoubanID: m.DoubanID, Title: m.Title, Poster: m.Poster, Year: m.Year, Summary: m.Summary, Actors: m.Actors, Directors: m.Directors, Genres: m.Genres, Countries: m.Countries, MediaType: m.MediaType, RatingDouban: m.RatingDouban}
		}
	}
	for index := range items {
		item := &items[index]
		item.VodPlayUrl = playurl.Clean(item.VodPlayUrl, item.VodRemarks)
		if item.VodPlayUrl == "" || item.ResourceStatus == "removed" || item.ResourceStatus == "retired" || item.ResourceStatus == "deleted" {
			continue
		}
		list = append(list, buildTVBoxVOD(item.SourceKey+":"+item.VodId, item, canonical[item.MediaID]))
	}
	total := len(list)
	const pageSize = 20
	pageCount := (total + pageSize - 1) / pageSize
	if pageCount < 1 {
		pageCount = 1
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 1, "msg": "数据列表", "page": page, "pagecount": pageCount,
		"limit": "20", "total": total, "list": list[start:end],
	})
}

// tvboxDetail 返回详情，ids 形如 sourceKey:vodID；douban:xxx 走豆瓣 ID 分支。
func (handler *Handler) tvboxDetail(c *gin.Context, ids string) {
	if ids == "test" {
		c.JSON(http.StatusOK, tvboxTestPayload())
		return
	}
	parts := strings.SplitN(ids, ":", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无效的ID", "list": []gin.H{}})
		return
	}
	if parts[0] == "douban" {
		handler.tvboxDetailFromDouban(c, parts[1])
		return
	}
	item, err := handler.details.Get(c.Request.Context(), parts[0], parts[1])
	if err != nil || item == nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到内容", "list": []gin.H{}})
		return
	}
	if item.VodPlayUrl == "" {
		item, _ = handler.details.Refresh(c.Request.Context(), parts[0], parts[1])
	}
	if item == nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无播放链接", "list": []gin.H{}})
		return
	}
	_, playURL := formatTVBoxPlayURL(item.VodPlayUrl)
	if playURL == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无播放链接", "list": []gin.H{}})
		return
	}
	c.JSON(http.StatusOK, listPayload([]gin.H{buildTVBoxVOD(ids, item,
		handler.resolveDisplayMedia(c.Request.Context(), item, item.VodDoubanId))}))
}

// tvboxDetailFromDouban 用豆瓣 ID 找可播放资源，找不到就用片名再搜一次。
func (handler *Handler) tvboxDetailFromDouban(c *gin.Context, doubanID string) {
	items, _ := handler.catalog.SearchByDoubanID(c.Request.Context(), doubanID)
	if playable := firstPlayable(items); playable != nil {
		c.JSON(http.StatusOK, listPayload([]gin.H{buildTVBoxVOD(playable.SourceKey+":"+playable.VodId, playable,
			handler.resolveDisplayMedia(c.Request.Context(), playable, doubanID))}))
		return
	}
	if handler.titleFinder != nil {
		title, _ := handler.titleFinder.FindTitleByDoubanID(c.Request.Context(), doubanID)
		if title != "" {
			items, _ = handler.catalog.Search(c.Request.Context(), title)
			if playable := firstPlayable(items); playable != nil {
				c.JSON(http.StatusOK, listPayload([]gin.H{buildTVBoxVOD(playable.SourceKey+":"+playable.VodId, playable,
					handler.resolveDisplayMedia(c.Request.Context(), playable, doubanID))}))
				return
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到播放源", "list": []gin.H{}})
}

// firstPlayable 取第一条有播放地址的资源。
func firstPlayable(items []search.VodItem) *search.VodItem {
	for index := range items {
		if _, url := formatTVBoxPlayURL(items[index].VodPlayUrl); url != "" {
			return &items[index]
		}
	}
	return nil
}

// tvboxCategory 分类页，映射到对应的热门榜。
func (handler *Handler) tvboxCategory(c *gin.Context, rawTypeID string) {
	typeID, _ := strconv.Atoi(rawTypeID)
	mediaType := map[int]string{1: "movie", 2: "tv", 3: "show", 4: "cartoon"}[typeID]
	if mediaType == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": 0, "list": []gin.H{}})
		return
	}
	handler.tvboxPopular(c, mediaType, false)
}

// tvboxPopular 把热门榜转成 TVBox 列表格式。
func (handler *Handler) tvboxPopular(c *gin.Context, mediaType string, includeClasses bool) {
	subjects, err := handler.popular.Popular(c.Request.Context(), mediaType)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("TVBox popular fetch failed", "media_type", mediaType, "error", err)
	}
	list := make([]gin.H, 0, len(subjects))
	for _, subject := range subjects {
		pic := subject.Cover
		if strings.HasPrefix(pic, "/") {
			pic = requestBaseURL(c) + pic
		}
		list = append(list, gin.H{
			"vod_id": "douban:" + subject.ID, "type_id": 0, "type_name": "", "vod_name": subject.Title,
			"vod_pic": pic, "vod_lang": "", "vod_area": "", "vod_year": "", "vod_remarks": subject.EpisodesInfo,
			"vod_actor": "", "vod_director": "", "vod_content": "", "vod_blurb": "", "vod_tag": "", "vod_time": "",
			"vod_play_from": "", "vod_play_url": "",
		})
	}
	payload := gin.H{"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": len(list), "list": list}
	if includeClasses {
		payload["class"] = tvboxCategories
	}
	c.JSON(http.StatusOK, payload)
}

// buildTVBoxVOD 组装 TVBox 的单条数据。
func buildTVBoxVOD(vodID string, item *search.VodItem, media *mediaidentity.Media) gin.H {
	playFrom, playURL := formatTVBoxPlayURL(item.VodPlayUrl)
	view := buildPlayView(media, item)
	genres, countries := item.VodTag, item.VodArea
	typeName := item.TypeName
	if media != nil {
		genres, countries = "", ""
		typeName = map[string]string{"movie": "电影", "tv": "电视剧", "show": "综艺", "cartoon": "动漫"}[media.MediaType]
	}
	if len(view.Genres) > 0 {
		genres = strings.Join(view.Genres, ",")
	}
	if len(view.Countries) > 0 {
		countries = strings.Join(view.Countries, ",")
	}
	return gin.H{
		"vod_id": vodID, "type_id": tvboxTypeNameToID(typeName), "type_name": typeName,
		"vod_name": view.Title, "vod_pic": view.Poster, "vod_lang": item.VodLang, "vod_area": countries,
		"vod_year": view.Year, "vod_remarks": item.VodRemarks, "vod_actor": view.Actors,
		"vod_director": view.Directors, "vod_content": view.Summary, "vod_blurb": view.Summary,
		"vod_tag": genres, "vod_time": item.VodTime, "vod_play_from": playFrom, "vod_play_url": playURL,
	}
}

// listPayload 包一层 TVBox 的列表返回格式。
func listPayload(list []gin.H) gin.H {
	return gin.H{"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": len(list), "list": list}
}

// tvboxCategories 是 TVBox 首页的固定分类。
var tvboxCategories = []gin.H{
	{"type_id": 1, "type_name": "电影"}, {"type_id": 2, "type_name": "电视剧"},
	{"type_id": 3, "type_name": "综艺"}, {"type_id": 4, "type_name": "动漫"},
}

// tvboxTypeNameToID 分类名转 TVBox 分类 ID。
func tvboxTypeNameToID(name string) int {
	return map[string]int{"电影": 1, "电视剧": 2, "综艺": 3, "动漫": 4}[name]
}

// requestBaseURL 从请求头推断站点地址（支持反向代理的 X-Forwarded-Proto）。
func requestBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

// tvboxTestPayload 是 ids=test 时返回的样例数据，用于 TVBox 联调。
func tvboxTestPayload() gin.H {
	return gin.H{
		"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": 1,
		"list": []gin.H{{
			"vod_id": "test", "type_id": 1, "type_name": "电影", "vod_name": "TVBox格式测试",
			"vod_pic":  "https://img9.doubanio.com/view/photo/s_ratio_poster/public/p2656327176.webp",
			"vod_lang": "国语", "vod_area": "中国大陆", "vod_year": "2024", "vod_remarks": "测试",
			"vod_actor": "测试演员", "vod_director": "测试导演", "vod_content": "这是一个TVBox格式测试视频",
			"vod_blurb": "测试简介", "vod_tag": "测试", "vod_time": "2024-01-01 00:00:00",
			"vod_play_from": "测试源", "vod_play_url": "第01集$https://test-stream.m3u8",
		}},
	}
}
