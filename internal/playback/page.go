package playback

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
)

type WatchEpisodeView struct {
	UnitID                             int
	EpisodeKey, EpisodeLabel, PlayLink string
	HasResource, IsPlaying             bool
}

func (handler *Handler) play(c *gin.Context) {
	handler.renderPlayback(c, "play", c.Query("douban_id"), c.Param("source_key"), c.Param("vod_id"))
}

func (handler *Handler) watch(c *gin.Context) {
	handler.renderPlayback(c, "watch", c.Param("douban_id"), c.Query("source_key"), c.Query("vod_id"))
}

// renderPlayback 两种入口只决定资源范围，后续资料、选集、地址选择及模板完全复用。
func (handler *Handler) renderPlayback(c *gin.Context, entry, doubanID, sourceKey, vodID string) {
	var canonical *mediaidentity.Media
	var detail *search.VodItem
	if entry == "watch" && handler.media != nil {
		if media, err := handler.media.FindByDoubanID(c.Request.Context(), doubanID); err == nil && media.ID > 0 {
			canonical = &media
		}
	}
	if sourceKey != "" && vodID != "" {
		var err error
		detail, err = handler.details.Get(c.Request.Context(), sourceKey, vodID)
		if err != nil {
			requestmeta.Logger(c.Request.Context()).Warn("load selected playback resource", "error", err)
		}
		if canonical == nil && detail != nil {
			canonical = handler.resolveDisplayMedia(c.Request.Context(), detail, doubanID)
		}
	}
	if canonical == nil && detail == nil {
		c.HTML(http.StatusNotFound, "404.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "视频未找到"}, nil))
		return
	}
	if doubanID == "" && detail != nil {
		doubanID = detail.VodDoubanId
	}
	view := buildPlayView(canonical, detail)
	if handler.copyright != nil {
		if blocked, _ := handler.copyright.IsCopyrightRestricted(c.Request.Context(), view.Title); blocked {
			c.Redirect(http.StatusFound, "/copyright-restricted?title="+url.QueryEscape(view.Title))
			return
		}
	}
	mediaID, kind, title := 0, "", ""
	if canonical != nil {
		mediaID, kind, title = canonical.ID, canonical.MediaType, canonical.Title
		doubanID = canonical.DoubanID
	}
	if detail != nil && kind == "" {
		kind = detail.TypeName
		title = detail.VodName
	}
	var infos []mediaidentity.EpisodeInfo
	if mediaID > 0 && handler.episodes != nil {
		var err error
		infos, err = handler.episodes.ListAllEpisodes(c.Request.Context(), mediaID)
		if err != nil {
			handler.playbackReadError(c, err)
			return
		}
	}
	var direct []SourceCandidate
	// 指名的资源必须属于当前作品；没有规范身份的 /play 则按来源自己的列表播放。
	if detail != nil && (canonical == nil || detail.MediaID == mediaID || doubanID != "" && detail.VodDoubanId == doubanID) {
		resourceTitle := detail.VodName
		if playurl.SeasonFromTitle(resourceTitle) == 0 {
			resourceTitle = title
		}
		for _, e := range mediaidentity.ParseResourceEpisodes(sourceKey, vodID, mediaID, kind, detail.VodPlayUrl, resourceTitle) {
			for _, info := range infos {
				if info.SeasonNumber == e.SeasonNumber && info.EpisodeKey == e.EpisodeKey {
					e.MediaUnitID = info.UnitID
					break
				}
			}
			direct = append(direct, sourceCandidate(mediaidentity.ResourceCandidate{Episode: e,
				SuccessCount: detail.SampleCount - detail.FailedCount, FailureCount: detail.FailedCount, AvgLoadMs: detail.AvgSpeedMs, MappingConfidence: detail.MediaConfidence}))
		}
	}
	if len(infos) == 0 {
		seen := map[string]bool{}
		for _, e := range direct {
			if seen[fmt.Sprintf("%d:%s", e.SeasonNumber, e.EpisodeKey)] {
				continue
			}
			seen[fmt.Sprintf("%d:%s", e.SeasonNumber, e.EpisodeKey)] = true
			label := e.EpisodeLabel
			if e.EpisodeKey == "feature" {
				label = "正片"
			}
			infos = append(infos, mediaidentity.EpisodeInfo{UnitID: e.MediaUnitID, SeasonNumber: e.SeasonNumber, EpisodeKey: e.EpisodeKey, EpisodeLabel: label, HasResource: true, SourceCount: 1})
		}
	}
	requested := strings.TrimSpace(c.Query("ep"))
	unitID, _ := strconv.Atoi(c.Query("unit"))
	season, key := selectContent(infos, unitID, requested)
	var candidates []SourceCandidate
	if mediaID > 0 && handler.episodes != nil && key != "" {
		raw, err := handler.episodes.ListResourceCandidates(c.Request.Context(), mediaID, season, key)
		if err != nil {
			handler.playbackReadError(c, err)
			return
		}
		for _, candidate := range raw {
			candidates = append(candidates, sourceCandidate(candidate))
		}
	}
	// 指定资源未挂到单元时也用同一解析结果，绝不在播放请求里补写候选索引。
	seen := map[string]bool{}
	for _, e := range candidates {
		seen[e.CandidateKey] = true
	}
	for _, e := range direct {
		if e.SeasonNumber == season && e.EpisodeKey == key && !seen[e.CandidateKey] {
			candidates = append(candidates, e)
		}
	}
	candidates = RankSameEpisode(candidates, season, key)
	part := c.Query("part")
	if part == "" {
		part = playurl.MoviePart(requested)
	}
	best, found := selectSource(candidates, sourceKey, vodID, c.Query("source"), c.Query("ver"), c.Query("candidate"), part)
	episode := key
	for _, info := range infos {
		if info.EpisodeKey == key && info.SeasonNumber == season {
			episode = info.EpisodeLabel
			break
		}
	}
	if key == "feature" {
		episode = "正片"
	}
	if key == "" {
		episode = requested
	}
	if found {
		if best.Part != "" {
			episode = "正片（" + best.Part + "）"
		}
		unitID = best.MediaUnitID
		sourceKey, vodID = best.SourceKey, best.VodID
	}
	var grid []WatchEpisodeView
	for _, info := range infos {
		q := url.Values{"ep": {info.EpisodeKey}}
		if info.UnitID > 0 {
			q.Set("unit", strconv.Itoa(info.UnitID))
		}
		path := playbackPath(entry, doubanID, sourceKey, vodID)
		grid = append(grid, WatchEpisodeView{UnitID: info.UnitID, EpisodeKey: info.EpisodeKey, EpisodeLabel: info.EpisodeLabel,
			HasResource: info.HasResource, IsPlaying: info.EpisodeKey == key && info.SeasonNumber == season, PlayLink: path + "?" + q.Encode()})
	}
	sources := buildEpisodeSources(candidates, sourceKey, vodID, best.PlayURL, episode, doubanID)
	for i, e := range candidates {
		q := url.Values{"ep": {key}, "source_key": {e.SourceKey}, "vod_id": {e.VodID}, "candidate": {e.CandidateKey}}
		if e.MediaUnitID > 0 {
			q.Set("unit", strconv.Itoa(e.MediaUnitID))
		}
		if e.Part != "" {
			q.Set("part", e.Part)
		}
		sources[i].PlayLink = playbackPath(entry, doubanID, e.SourceKey, e.VodID) + "?" + q.Encode()
	}
	pageTitle := "《" + view.Title + "》"
	showGrid := len(grid) > 1 || (len(grid) == 1 && key != "feature")
	if showGrid && episode != "" {
		pageTitle += "(" + episode + ")"
	}
	pageTitle += " - 在线播放免费高清线路 - " + handler.config.SiteName
	message := "暂无可用播放链接"
	if !found && (sourceKey != "" || c.Query("ver") != "") && len(candidates) > 0 {
		message = "所选线路或版本已不可用，请选择其他来源"
	}
	extra := gin.H{"View": view, "DoubanID": doubanID, "MediaID": mediaID, "MediaUnitID": unitID, "EntryPage": entry,
		"CandidateKey": best.CandidateKey, "PlaybackVersion": best.PlaybackVersion, "Part": best.Part,
		"SeasonNumber": season, "EpisodeKey": key, "Episode": episode, "PlayURL": best.PlayURL, "EmptyMessage": message,
		"SourceKey": sourceKey, "VodID": vodID, "SourceLabel": best.SourceKey + " · " + best.LineLabel,
		"EpisodeGrid": grid, "ShowEpisodeGrid": showGrid, "EpisodeSources": sources, "AutoFailoverEnabled": true,
		"LoggedIn": auth.UserID(c) > 0, "ContentClass": "full-width"}
	c.HTML(http.StatusOK, "watch.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: pageTitle, Description: fmt.Sprintf("在线观看 %s - %s", view.Title, handler.config.SiteName), Cover: view.Poster, Robots: "noindex, follow"}, extra))
}

func playbackPath(entry, douban, source, vod string) string {
	if entry == "watch" && douban != "" {
		return "/watch/" + url.PathEscape(douban)
	}
	return "/play/" + url.PathEscape(source) + "/" + url.PathEscape(vod)
}

// selectContent 旧电影版本名仅兼容到正片，不能把不存在的剧集兜底成第一集。
func selectContent(infos []mediaidentity.EpisodeInfo, unitID int, ep string) (int, string) {
	season, key := mediaidentity.NormalizeEpisodeLabel(ep)
	if unitID > 0 {
		for _, u := range infos {
			if u.UnitID == unitID {
				return u.SeasonNumber, u.EpisodeKey
			}
		}
		return season, ""
	}
	for _, u := range infos {
		if ep != "" && (u.EpisodeKey == key || u.EpisodeLabel == ep) {
			return u.SeasonNumber, u.EpisodeKey
		}
	}
	if ep == "" {
		for _, u := range infos {
			if u.HasResource {
				return u.SeasonNumber, u.EpisodeKey
			}
		}
		if len(infos) > 0 {
			return infos[0].SeasonNumber, infos[0].EpisodeKey
		}
	}
	if ep == "" || playurl.IsVersion(ep) || playurl.MoviePart(ep) != "" || ep == "S01E01" {
		for _, u := range infos {
			if u.EpisodeKey == "feature" {
				return 0, "feature"
			}
		}
	}
	return season, key
}

func selectSource(candidates []SourceCandidate, source, vod, line, version, key, part string) (SourceCandidate, bool) {
	var eligible []SourceCandidate
	for _, c := range candidates {
		if source != "" && c.SourceKey != source || vod != "" && c.VodID != vod || line != "" && c.LineLabel != line || version != "" && c.Quality != version || key != "" && c.CandidateKey != key || part != "" && c.Part != part {
			continue
		}
		eligible = append(eligible, c)
	}
	// 未指名分段时优先完整版；只有分段资源时从上段开始，不能按速度跳到下段。
	if part == "" && key == "" {
		for _, preferred := range []string{"", "上", "中", "下"} {
			for _, c := range eligible {
				if c.Part == preferred {
					return c, true
				}
			}
		}
	}
	if len(eligible) > 0 {
		return eligible[0], true
	}
	return SourceCandidate{}, false
}

func (handler *Handler) playbackReadError(c *gin.Context, err error) {
	requestmeta.Logger(c.Request.Context()).Error("read playback resources", "error", err)
	c.String(http.StatusServiceUnavailable, "播放资源暂时无法读取，请稍后重试")
}
