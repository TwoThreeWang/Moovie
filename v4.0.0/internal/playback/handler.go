package playback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/ratelimit"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
)

// PlayViewInfo 是播放页统一 ViewModel。它把可用的规范媒体资料与原始 vod_item 详情合并，
// 模板不需要再实现两套数据源的条件判断。
type PlayViewInfo struct {
	Title     string
	Poster    string
	Rating    float64
	Year      string
	Genres    []string
	Countries []string
	Directors string
	Actors    string
	Summary   string
}

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
	QualityLabel string // 稳定/一般/不稳定/待验证
	QualityClass string // stable/normal/unstable/unknown
	SpeedLabel   string // "0.8秒" or ""
	SpeedClass   string // fast/normal/slow or ""
	PlayLink     string // /play/sourceKey/vodID?ep=...&douban_id=...
	IsCurrent    bool
}

// buildPlayView 合并两份资料：先用资源站详情打底，再用规范媒体资料覆盖展示字段。
// 播放地址等资源信息始终来自资源站。
func buildPlayView(media *mediaidentity.Media, detail *search.VodItem) PlayViewInfo {
	view := PlayViewInfo{
		Title:   detail.VodName,
		Poster:  detail.VodPic,
		Year:    detail.VodYear,
		Summary: detail.VodContent,
	}
	if genres := detail.GetGenres(); len(genres) > 0 {
		view.Genres = genres
	}
	if detail.VodArea != "" {
		view.Countries = []string{detail.VodArea}
	}
	if directors := detail.GetDirectors(); len(directors) > 0 {
		view.Directors = strings.Join(directors, " / ")
	}
	if actors := detail.GetActors(); len(actors) > 0 {
		view.Actors = strings.Join(actors, " / ")
	}

	if media == nil {
		return view
	}
	// 有规范媒体资料时覆盖展示字段，但保留资源播放信息。
	if media.Title != "" {
		view.Title = media.Title
	}
	if media.Poster != "" {
		view.Poster = media.Poster
	}
	if media.RatingDouban > 0 {
		view.Rating = media.RatingDouban
	}
	if media.Year != "" {
		view.Year = media.Year
	}
	if media.Genres != "" {
		view.Genres = splitTrimmed(media.Genres)
	}
	if media.Countries != "" {
		view.Countries = splitTrimmed(media.Countries)
	}
	if media.Directors != "" {
		view.Directors = parsePeopleJSON(media.Directors, 3)
	}
	if media.Actors != "" {
		view.Actors = parsePeopleJSON(media.Actors, 5)
	}
	if media.Summary != "" {
		view.Summary = media.Summary
	}
	return view
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
		sources = append(sources, EpisodeSourceView{
			SourceKey: c.SourceKey, VodID: c.VodID, LineLabel: c.LineLabel, SourceLabel: label,
			QualityLabel: qualityLabel, QualityClass: qualityClass,
			SpeedLabel: speedLabel, SpeedClass: speedClass,
			PlayLink: playLink, IsCurrent: isCurrent,
		})
	}
	return sources
}

// episodeQualityInfo 把质量分转成中文标签：≥0.75 稳定，≥0.5 一般，否则不稳定；没样本是待验证。
func episodeQualityInfo(health PlaybackHealth) (string, string) {
	if health.Total() == 0 {
		return "待验证", "unknown"
	}
	score := health.Score()
	if score >= 0.75 {
		return "稳定", "stable"
	}
	if score >= 0.5 {
		return "一般", "normal"
	}
	return "不稳定", "unstable"
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
	config       config.Config
	catalog      Catalog
	details      *DetailService
	popular      PopularProvider
	titleFinder  MovieTitleFinder
	speeds       SpeedStore
	copyright    CopyrightChecker
	userMovies   UserMovieStore
	media        mediaidentity.Resolver
	episodes     mediaidentity.EpisodeReader
	events       mediaidentity.PlaybackEventWriter
	airSchedule  AirScheduleReader
	eventLimiter *ratelimit.PerIP
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
	router.GET("/api/watch/resolve", handler.resolveWatchURL)
	router.GET("/api/v2/media/:id/resources", handler.resources)
	router.GET("/api/v2/media-units/:unit_id/playback-candidates", handler.playbackCandidatesV2)
	router.POST("/api/v2/playback/events", handler.playbackEventV2)
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
			"candidate_id": source.CandidateID, "play_line_id": source.LineID,
			"line_key": source.LineKey, "line_label": source.LineLabel,
			"source_key": source.SourceKey, "vod_id": source.VodID, "play_url": source.PlayURL,
			"episode_key": source.EpisodeKey, "episode_label": source.EpisodeLabel,
			"score": source.Score(), "quality_label": playbackQualityLabel(source.Health),
			"mapping_confidence": source.MappingConfidence,
		})
	}
	c.JSON(http.StatusOK, gin.H{"media_id": mediaID, "unit_id": unitID, "resume_position": 0,
		"candidate_session_id": newCandidateSessionID(),
		// player.js 依据该字段决定是否自动换源，自动换源已固定启用。
		"auto_failover_enabled": true, "candidates": items})
}

// playbackEventRequest 是播放器上报的事件体。
type playbackEventRequest struct {
	AttemptID          string `json:"attempt_id"`
	CandidateSessionID string `json:"candidate_session_id"`
	EventType          string `json:"event_type"`
	CandidateID        int    `json:"candidate_id"`
	MediaUnitID        int    `json:"media_unit_id"`
	SourceKey          string `json:"source_key"`
	VodID              string `json:"vod_id"`
	ElapsedMs          int    `json:"elapsed_ms"`
	Reason             string `json:"reason"`
}

// playbackEventV2 接收播放器上报的质量事件（先限流，再校验，最后落库）。
func (handler *Handler) playbackEventV2(c *gin.Context) {
	if !handler.eventLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, "播放事件上报过于频繁")
		return
	}
	var request playbackEventRequest
	if handler.events == nil {
		apiError(c, http.StatusServiceUnavailable, "播放事件服务暂时不可用")
		return
	}
	if c.ShouldBindJSON(&request) != nil {
		apiError(c, http.StatusBadRequest, "播放事件参数错误")
		return
	}
	accepted, err := handler.events.RecordPlaybackEvent(c.Request.Context(), mediaidentity.PlaybackAttemptEvent{
		AttemptID: request.AttemptID, CandidateSessionID: request.CandidateSessionID,
		EventType: request.EventType, CandidateID: request.CandidateID,
		MediaUnitID: request.MediaUnitID, SourceKey: request.SourceKey, VodID: request.VodID,
		ElapsedMs: request.ElapsedMs, Reason: request.Reason,
	})
	if err != nil {
		if errors.Is(err, mediaidentity.ErrInvalidPlaybackEvent) {
			apiError(c, http.StatusBadRequest, "播放事件参数错误")
		} else {
			requestmeta.Logger(c.Request.Context()).Warn("playback event persistence failed",
				"attempt_id", request.AttemptID, "candidate_session_id", request.CandidateSessionID,
				"candidate_id", request.CandidateID, "media_unit_id", request.MediaUnitID,
				"source_key", request.SourceKey, "vod_id", request.VodID, "event_type", request.EventType, "error", err)
			apiError(c, http.StatusInternalServerError, "播放事件保存失败")
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"accepted": accepted})
}

// sourceCandidate 把存储层候选转成本包的排序用结构。
func sourceCandidate(candidate mediaidentity.ResourceCandidate) SourceCandidate {
	return SourceCandidate{CandidateID: candidate.CandidateID, LineID: candidate.LineID,
		LineKey: candidate.LineKey, LineLabel: candidate.LineLabel,
		SourceKey: candidate.SourceKey, VodID: candidate.VodID, MediaID: candidate.MediaID, MediaUnitID: candidate.MediaUnitID,
		SeasonNumber: candidate.SeasonNumber, EpisodeKey: candidate.EpisodeKey, EpisodeLabel: candidate.EpisodeLabel, PlayURL: candidate.PlayURL,
		MappingConfidence: candidate.MappingConfidence,
		Health:            PlaybackHealth{SuccessCount: candidate.SuccessCount, FailureCount: candidate.FailureCount, AvgLoadMs: candidate.AvgLoadMs}}
}

// playbackQualityLabel 质量分对应的中文标签（接口版）。
func playbackQualityLabel(health PlaybackHealth) string {
	if health.Total() == 0 {
		return "待验证"
	}
	if health.Score() >= 0.75 {
		return "稳定"
	}
	if health.Score() >= 0.5 {
		return "一般"
	}
	return "不稳定"
}

// newCandidateSessionID 生成一次播放会话 ID，用于把多条上报事件串成一次播放尝试。
func newCandidateSessionID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buffer)
}

// play 是按资源入口的播放页 /play/:source_key/:vod_id。
func (handler *Handler) play(c *gin.Context) {
	handler.renderPlayPage(c, c.Param("source_key"), c.Param("vod_id"), c.Query("douban_id"))
}

// renderPlayPage 渲染资源直连播放页：
// 抓详情 → 选线路和集 → 找规范媒体 → 顺手建立剧集索引 → 取同集候选排序 → 渲染。
// 三个输入单独传而不是从路由参数里取，是为了让 /watch 在走不通时能直接复用这套流程，
// 不必把同样的逻辑再抄一遍（见 fallbackToPlay）。
func (handler *Handler) renderPlayPage(c *gin.Context, sourceKey, vodID, doubanID string) {
	detail, err := handler.details.Get(c.Request.Context(), sourceKey, vodID)
	if err != nil || detail == nil {
		c.HTML(http.StatusNotFound, "404.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "视频未找到 - " + handler.config.SiteName}, nil))
		return
	}

	sources := parsePlayURL(detail.VodPlayUrl)
	var currentSource *PlaySource
	if len(sources) > 0 {
		currentSource = &sources[0]
		if requested := c.Query("source"); requested != "" {
			for index := range sources {
				if sources[index].Name == requested {
					currentSource = &sources[index]
					break
				}
			}
		}
	}

	episode, playURL := c.Query("ep"), ""
	if currentSource != nil {
		if episode == "" && len(currentSource.Episodes) > 0 {
			episode = currentSource.Episodes[0].Title
			playURL = currentSource.Episodes[0].URL
		} else {
			if selected, ok := selectEpisode(currentSource.Episodes, episode); ok {
				playURL = selected.URL
			}
		}
	}

	if doubanID == "" {
		doubanID = detail.VodDoubanId
	}
	seasonNumber, episodeKey := mediaidentity.NormalizeEpisodeLabel(episode)
	userID := auth.UserID(c)
	canonicalMedia := handler.resolveDisplayMedia(c.Request.Context(), detail, doubanID)
	mediaID, mediaType := 0, ""
	if canonicalMedia != nil {
		mediaID, mediaType = canonicalMedia.ID, canonicalMedia.MediaType
		if canonicalMedia.DoubanID != "" {
			doubanID = canonicalMedia.DoubanID
		}
	}
	if writer, ok := handler.media.(mediaidentity.EpisodeWriter); ok && mediaID > 0 {
		episodeRows := mediaidentity.ParseResourceEpisodes(sourceKey, vodID, mediaID, mediaType, detail.VodPlayUrl)
		if err := writer.UpsertEpisodes(c.Request.Context(), episodeRows); err != nil {
			requestmeta.Logger(c.Request.Context()).Warn("resource episode index update failed",
				"media_id", mediaID, "source_key", sourceKey, "vod_id", vodID, "error", err)
		}
	}
	candidateID, mediaUnitID := 0, 0
	var allCandidates []SourceCandidate
	if handler.episodes != nil && mediaID > 0 && episodeKey != "" {
		if rawCandidates, candidateErr := handler.episodes.ListResourceCandidates(c.Request.Context(), mediaID, seasonNumber, episodeKey); candidateErr == nil {
			for _, candidate := range rawCandidates {
				sc := sourceCandidate(candidate)
				allCandidates = append(allCandidates, sc)
				if candidate.SourceKey == sourceKey && candidate.VodID == vodID && candidate.PlayURL == playURL {
					candidateID, mediaUnitID = candidate.CandidateID, candidate.MediaUnitID
				}
			}
		}
	}
	isWatched := false
	if userID > 0 && doubanID != "" && handler.userMovies != nil {
		isWatched, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, doubanID, "watched")
	}
	if handler.copyright != nil {
		if blocked, _ := handler.copyright.IsCopyrightRestricted(c.Request.Context(), detail.VodName); blocked {
			c.Redirect(http.StatusFound, "/copyright-restricted?title="+url.QueryEscape(detail.VodName))
			return
		}
	}

	var loadStats *search.LoadStats
	if handler.speeds != nil {
		loadStats, _ = handler.speeds.LoadStats(c.Request.Context(), sourceKey, vodID)
	}
	// 构建统一页面模型，以及限定到当前剧集的备选来源。
	view := buildPlayView(canonicalMedia, detail)
	title := "《" + view.Title + "》"
	if episode != "" {
		title += "(" + episode + ")"
	}
	title += " - 在线播放免费高清线路 - " + handler.config.SiteName
	ranked := filterSameEpisode(allCandidates, seasonNumber, episodeKey)
	ranked = RankSameEpisode(ranked, seasonNumber, episodeKey)
	episodeSources := buildEpisodeSources(ranked, sourceKey, vodID, playURL, episode, doubanID)
	extra := gin.H{
		"DoubanID": doubanID, "MediaID": mediaID, "MediaUnitID": mediaUnitID, "CandidateID": candidateID,
		"CandidateSessionID": newCandidateSessionID(),
		"SeasonNumber":       seasonNumber, "EpisodeKey": episodeKey,
		"IsWatched": isWatched, "LoggedIn": userID > 0,
		"VodID": vodID, "SourceKey": sourceKey, "Detail": detail, "Sources": sources,
		"CurrentSource": currentSource, "Episode": episode, "PlayURL": playURL,
		"ContentClass": "full-width", "LoadStats": loadStats,
		"AutoFailoverEnabled": true,
		"View":                view,
		"EpisodeSources":      episodeSources,
		"AirSchedule":         handler.airScheduleView(c.Request.Context(), canonicalMedia),
	}
	if currentSource != nil {
		extra["Episodes"] = currentSource.Episodes
		extra["Source"] = currentSource.Name
	}
	c.HTML(http.StatusOK, "play.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: title, Description: fmt.Sprintf("在线观看 %s - %s", view.Title, handler.config.SiteName),
		Keywords: fmt.Sprintf("%s,在线播放,高清视频,%s", view.Title, handler.config.SiteName), Cover: view.Poster,
	}, extra))
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

// WatchEpisodeView 表示选集网格中的一个单元格。
type WatchEpisodeView struct {
	EpisodeKey   string
	EpisodeLabel string
	SourceCount  int
	IsPlaying    bool
}

// watch 是按豆瓣 ID 入口的播放页 /watch/:douban_id，步骤见下面的编号注释。
// 与 play 的区别：它先定媒体再挑最优资源，所以能跨资源站自动选最好的线路；
// 但它依赖候选索引已经建好，没有候选就 302 回搜索页。
func (handler *Handler) watch(c *gin.Context) {
	doubanID := c.Param("douban_id")
	if doubanID == "" || doubanID == "0" {
		c.Redirect(http.StatusFound, "/")
		return
	}

	// 1. 解析规范媒体身份。解析不出来时，URL 上如果指名了资源就直接按 /play 播，
	//    否则回搜索页——优先用影片标题做关键词，裸豆瓣 ID 搜不出东西。
	searchKeyword := doubanID
	if handler.titleFinder != nil {
		if title, _ := handler.titleFinder.FindTitleByDoubanID(c.Request.Context(), doubanID); title != "" {
			searchKeyword = title
		}
	}
	if handler.media == nil {
		if handler.fallbackToPlay(c, doubanID) {
			return
		}
		c.Redirect(http.StatusFound, "/search?kw="+url.QueryEscape(searchKeyword))
		return
	}
	canonical, err := handler.media.FindByDoubanID(c.Request.Context(), doubanID)
	if err != nil || canonical.ID == 0 {
		if handler.fallbackToPlay(c, doubanID) {
			return
		}
		c.Redirect(http.StatusFound, "/search?kw="+url.QueryEscape(searchKeyword))
		return
	}

	// 2. 确定用户请求的季集。未指定 ep 时使用数据库中第一个真实单元，
	// 不能假定电影都叫 S01E01；常见的电影资源键是“正片”或“HD”。
	epParam := strings.TrimSpace(c.Query("ep"))
	var episodeInfos []mediaidentity.EpisodeInfo
	if handler.episodes != nil {
		episodeInfos, _ = handler.episodes.ListAllEpisodes(c.Request.Context(), canonical.ID)
	}
	seasonNumber, episodeKey := mediaidentity.NormalizeEpisodeLabel(epParam)
	if epParam == "" && len(episodeInfos) > 0 {
		seasonNumber, episodeKey = episodeInfos[0].SeasonNumber, episodeInfos[0].EpisodeKey
		epParam = episodeInfos[0].EpisodeLabel
		if epParam == "" {
			epParam = episodeKey
		}
	}

	// 3. 使用已经通过 /play 访问并建立索引的关联资源候选。
	//    watch 页面本身不会触发索引；索引只在用户访问 /play 或 Worker 处理资源时延迟完成。

	// 4. 获取当前剧集候选并排序。
	var allCandidates []SourceCandidate
	if handler.episodes != nil {
		if raw, queryErr := handler.episodes.ListResourceCandidates(c.Request.Context(), canonical.ID, seasonNumber, episodeKey); queryErr == nil {
			for _, rc := range raw {
				allCandidates = append(allCandidates, sourceCandidate(rc))
			}
		}
	}
	ranked := filterSameEpisode(allCandidates, seasonNumber, episodeKey)
	ranked = RankSameEpisode(ranked, seasonNumber, episodeKey)

	// 5. 没有候选时，先用 URL 上的 source_key/vod_id 现场补一次索引再重试。
	//    剧集索引只在搜索和 /play 时写入，而搜索结果可以直接链到 /watch，
	//    因此用 URL 里已选中的资源现场补录后再查一遍。
	if len(ranked) == 0 && handler.indexWatchResource(c.Request.Context(), canonical, c.Query("source_key"), c.Query("vod_id")) {
		if epParam == "" {
			episodeInfos, _ = handler.episodes.ListAllEpisodes(c.Request.Context(), canonical.ID)
			if len(episodeInfos) > 0 {
				seasonNumber, episodeKey = episodeInfos[0].SeasonNumber, episodeInfos[0].EpisodeKey
				epParam = episodeInfos[0].EpisodeLabel
				if epParam == "" {
					epParam = episodeKey
				}
			}
		}
		allCandidates = allCandidates[:0]
		if raw, queryErr := handler.episodes.ListResourceCandidates(c.Request.Context(), canonical.ID, seasonNumber, episodeKey); queryErr == nil {
			for _, rc := range raw {
				allCandidates = append(allCandidates, sourceCandidate(rc))
			}
		}
		ranked = RankSameEpisode(filterSameEpisode(allCandidates, seasonNumber, episodeKey), seasonNumber, episodeKey)
	}

	// 6. 仍然没有候选时，用 resource_media_links 找一条已关联的资源现场补录索引。
	//    搜索结果已经知道"这部片有哪些资源"，只是剧集索引还没建好。
	if len(ranked) == 0 {
		if resolver, ok := handler.media.(linkedMediaResolver); ok {
			if link, err := resolver.FindLinkedResource(c.Request.Context(), canonical.ID); err == nil &&
				handler.indexWatchResource(c.Request.Context(), canonical, link.SourceKey, link.VodID) {
				if epParam == "" {
					episodeInfos, _ = handler.episodes.ListAllEpisodes(c.Request.Context(), canonical.ID)
					if len(episodeInfos) > 0 {
						seasonNumber, episodeKey = episodeInfos[0].SeasonNumber, episodeInfos[0].EpisodeKey
						epParam = episodeInfos[0].EpisodeLabel
						if epParam == "" {
							epParam = episodeKey
						}
					}
				}
				allCandidates = allCandidates[:0]
				if raw, queryErr := handler.episodes.ListResourceCandidates(c.Request.Context(), canonical.ID, seasonNumber, episodeKey); queryErr == nil {
					for _, rc := range raw {
						allCandidates = append(allCandidates, sourceCandidate(rc))
					}
				}
				ranked = RankSameEpisode(filterSameEpisode(allCandidates, seasonNumber, episodeKey), seasonNumber, episodeKey)
			}
		}
	}

	// 7. 选择最佳候选。补录之后仍然没有候选，说明这条资源解析不出剧集结构
	//    （分集格式不认识、或者干脆没关联到这部片子）——但它本身仍然能播，
	//    这正是 /play 存在的场景，于是降级过去；实在没资源可播才回搜索页。
	if len(ranked) == 0 {
		if handler.fallbackToPlay(c, doubanID) {
			return
		}
		c.Redirect(http.StatusFound, "/search?kw="+url.QueryEscape(canonical.Title))
		return
	}
	best := ranked[0]
	// 查询参数可以强制选择具体来源，用于用户手动换源。
	if forceSource, forceVod := c.Query("source_key"), c.Query("vod_id"); forceSource != "" && forceVod != "" {
		for _, rc := range ranked {
			if rc.SourceKey == forceSource && rc.VodID == forceVod {
				best = rc
				break
			}
		}
	}

	// 7. 加载所选来源的 VodItem 详情。
	detail, err := handler.details.Get(c.Request.Context(), best.SourceKey, best.VodID)
	if err != nil || detail == nil {
		c.Redirect(http.StatusFound, "/search?kw="+url.QueryEscape(canonical.Title))
		return
	}

	// 8. 从所选来源的剧集列表中确定播放地址。
	sources := parsePlayURL(detail.VodPlayUrl)
	var currentSource *PlaySource
	if len(sources) > 0 {
		currentSource = &sources[0]
		// 优先查找与最佳候选 line key 一致的线路。
		for i := range sources {
			if sources[i].Name == best.LineLabel {
				currentSource = &sources[i]
				break
			}
		}
	}
	episode := epParam
	playURL := best.PlayURL
	if episode == "" && currentSource != nil && len(currentSource.Episodes) > 0 {
		episode = currentSource.Episodes[0].Title
		// 如果候选播放地址仍有效，则基于候选重新计算。
		if playURL == "" {
			playURL = currentSource.Episodes[0].URL
		}
	}

	// 9. 汇总所有来源的已知剧集，构建选集网格。
	var episodeGrid []WatchEpisodeView
	for _, ep := range episodeInfos {
		episodeGrid = append(episodeGrid, WatchEpisodeView{
			EpisodeKey: ep.EpisodeKey, EpisodeLabel: ep.EpisodeLabel,
			SourceCount: ep.SourceCount, IsPlaying: ep.EpisodeKey == episodeKey,
		})
	}
	// 候选无法生成选集网格时，退回当前来源自己的剧集列表。
	if len(episodeGrid) == 0 && currentSource != nil {
		for _, ep := range currentSource.Episodes {
			_, epKey := mediaidentity.NormalizeEpisodeLabel(ep.Title)
			episodeGrid = append(episodeGrid, WatchEpisodeView{
				EpisodeKey: epKey, EpisodeLabel: ep.Title,
				SourceCount: 1, IsPlaying: epKey == episodeKey,
			})
		}
	}

	// 10. 构建页面 ViewModel 和同集来源列表。
	view := buildPlayView(&canonical, detail)
	episodeSources := buildEpisodeSources(ranked, best.SourceKey, best.VodID, playURL, episode, doubanID)

	// 11. 执行版权关键词检查。
	if handler.copyright != nil {
		if blocked, _ := handler.copyright.IsCopyrightRestricted(c.Request.Context(), canonical.Title); blocked {
			c.Redirect(http.StatusFound, "/copyright-restricted?title="+url.QueryEscape(canonical.Title))
			return
		}
	}

	// 12. 加载用户想看、看过等状态。
	userID := auth.UserID(c)
	isWatched := false
	if userID > 0 && handler.userMovies != nil {
		isWatched, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, doubanID, "watched")
	}

	// 13. 创建播放候选会话，用于后续质量事件关联。
	candidateID, mediaUnitID := best.CandidateID, best.MediaUnitID

	title := "《" + canonical.Title + "》"
	if episode != "" {
		title += "(" + episode + ")"
	}
	title += " - 在线播放 - " + handler.config.SiteName

	c.HTML(http.StatusOK, "watch.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       title,
		Description: fmt.Sprintf("在线观看 %s - %s", canonical.Title, handler.config.SiteName),
		Keywords:    fmt.Sprintf("%s,在线播放,高清视频,%s", canonical.Title, handler.config.SiteName),
		Cover:       view.Poster,
	}, gin.H{
		"DoubanID":            doubanID,
		"MediaID":             canonical.ID,
		"MediaUnitID":         mediaUnitID,
		"CandidateID":         candidateID,
		"CandidateSessionID":  newCandidateSessionID(),
		"SeasonNumber":        seasonNumber,
		"EpisodeKey":          episodeKey,
		"IsWatched":           isWatched,
		"LoggedIn":            userID > 0,
		"SourceKey":           best.SourceKey,
		"VodID":               best.VodID,
		"Episode":             episode,
		"PlayURL":             playURL,
		"ContentClass":        "full-width",
		"View":                view,
		"EpisodeGrid":         episodeGrid,
		"EpisodeSources":      episodeSources,
		"SourceLabel":         best.SourceKey + " · " + best.LineLabel,
		"AutoFailoverEnabled": true,
		"AirSchedule":         handler.airScheduleView(c.Request.Context(), &canonical),
	}))
}

// indexWatchResource 为 /watch 现场补录一条资源的剧集索引，真正写入了才返回 true。
// 搜索结果可以在索引回填完成前直接进入 /watch。
// 这里复用 /play 的做法，用 URL 带来的 source_key/vod_id 取详情并解析入库。
func (handler *Handler) indexWatchResource(ctx context.Context, media mediaidentity.Media, sourceKey, vodID string) bool {
	if sourceKey == "" || vodID == "" || media.ID <= 0 || handler.episodes == nil || handler.details == nil {
		return false
	}
	writer, ok := handler.media.(mediaidentity.EpisodeWriter)
	if !ok {
		return false
	}
	detail, err := handler.details.Get(ctx, sourceKey, vodID)
	if err != nil || detail == nil || detail.VodPlayUrl == "" {
		return false
	}
	episodes := mediaidentity.ParseResourceEpisodes(sourceKey, vodID, media.ID, media.MediaType, detail.VodPlayUrl)
	if len(episodes) == 0 {
		return false
	}
	if err := writer.UpsertEpisodes(ctx, episodes); err != nil {
		requestmeta.Logger(ctx).Warn("watch resource episode index failed",
			"media_id", media.ID, "source_key", sourceKey, "vod_id", vodID, "error", err)
		return false
	}
	return true
}

// fallbackToPlay 在 /watch 走不通时改用 /play 的资源直连方式渲染，真渲染了才返回 true。
// URL 上带了 source_key+vod_id，就说明来源（搜索结果、换源链接）已经指名了要播哪条资源，
// 而这两个参数正好是 /play 需要的全部输入。此时把人 302 回搜索页是纯亏：
// 手上明明攥着一条能播的资源。关联不上元数据的资源本来就该走 /play。
//
// 先确认详情和播放地址都在再渲染：资源真的死了的话，回搜索页比给一个
// 「暂无可用播放链接」的空播放页有用。这次多查一次库只发生在本来就要 302 的路径上。
func (handler *Handler) fallbackToPlay(c *gin.Context, doubanID string) bool {
	sourceKey, vodID := c.Query("source_key"), c.Query("vod_id")
	if sourceKey == "" || vodID == "" || handler.details == nil {
		return false
	}
	detail, err := handler.details.Get(c.Request.Context(), sourceKey, vodID)
	if err != nil || detail == nil || detail.VodPlayUrl == "" {
		return false
	}
	handler.renderPlayPage(c, sourceKey, vodID, doubanID)
	return true
}

// resolveWatchURL 返回指定媒体和剧集组合的最佳播放地址。
// watch 页 JavaScript 用它在不刷新整个页面的情况下切换剧集或来源。
func (handler *Handler) resolveWatchURL(c *gin.Context) {
	mediaID, _ := strconv.Atoi(c.Query("media_id"))
	epParam := c.Query("ep")
	seasonNumber, episodeKey := mediaidentity.NormalizeEpisodeLabel(epParam)
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

	// 用户明确请求具体来源时优先使用该来源。
	best := ranked[0]
	if forceSource != "" && forceVodID != "" {
		for _, sc := range ranked {
			if sc.SourceKey == forceSource && sc.VodID == forceVodID {
				best = sc
				break
			}
		}
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
		"candidate_id":  best.CandidateID,
		"media_unit_id": best.MediaUnitID,
		"session_id":    newCandidateSessionID(),
		"sources":       sourcesJSON,
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
	}
	c.HTML(http.StatusOK, "iptv.html", platformweb.NewData(c, handler.config, metadata, gin.H{"ContentClass": "full-width"}))
}

// tvbox 是 TVBox 配置说明页。
func (handler *Handler) tvbox(c *gin.Context) {
	metadata := platformweb.Metadata{Title: "TVBox 配置指南 - " + handler.config.SiteName}
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
	for index := range items {
		list = append(list, buildTVBoxVOD(items[index].SourceKey+":"+items[index].VodId, &items[index],
			handler.resolveDisplayMedia(c.Request.Context(), &items[index], items[index].VodDoubanId)))
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
	if len(view.Genres) > 0 {
		genres = strings.Join(view.Genres, ",")
	}
	if len(view.Countries) > 0 {
		countries = strings.Join(view.Countries, ",")
	}
	return gin.H{
		"vod_id": vodID, "type_id": tvboxTypeNameToID(item.TypeName), "type_name": item.TypeName,
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
