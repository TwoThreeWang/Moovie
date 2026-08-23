package history

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	"github.com/gin-gonic/gin"
)

// TodayUpdateReader 查询给定媒体集合在某一天播出的剧集。
// 为 nil 时"今日更新"端点返回空内容，首页不显示该区块。
type TodayUpdateReader interface {
	ListDailyUpdatesForMedia(ctx context.Context, mediaIDs []int, day time.Time) ([]mediaidentity.MediaUnit, error)
}

// TodayUpdate 是首页"今日更新"卡片。
type TodayUpdate struct {
	MediaID      int
	DoubanID     string
	Title        string
	Poster       string
	EpisodeLabel string // S02E07
	EpisodeTitle string
	// WatchingLabel 是用户当前的观看位置，例如 "第 3 集"。
	// 它让"已更新到第 7 集但你看到第 3 集"这件事在首页就能被看到。
	WatchingLabel string
	ContinueURL   string
	Playable      bool
}

// todayUpdatesLimit 限制首页一次展示的更新条目。
// 首页是入口而非清单页；同一天有大量更新时截断比铺满整屏更有用。
const todayUpdatesLimit = 12

// todayUpdates 渲染首页"今日更新"区块。
//
// 无论未登录、无在看记录、还是今天没有任何更新，都返回空响应体：
// 区块外壳（标题和容器）在 partial 内部，因此空响应意味着首页完全不出现这一块，
// 不会留下一个孤零零的标题。
func (handler *Handler) todayUpdates(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 || handler.todayUpdateReader == nil {
		c.Status(http.StatusOK)
		return
	}
	records, _, err := handler.continueRecords(c, userID, todayUpdatesLimit*4, 0)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("today updates: load continue records failed",
			"user_id", userID, "error", err)
		c.Status(http.StatusOK)
		return
	}
	// 同一部剧可能有多条进度记录（不同资源站/不同集），按 media_id 去重后再查播出日期。
	// 只有已关联规范媒体的记录才有 media_id，纯资源站身份的进度无法对应到播出表。
	byMedia := make(map[int]Record, len(records))
	mediaIDs := make([]int, 0, len(records))
	for _, record := range records {
		if record.MediaID <= 0 {
			continue
		}
		if _, seen := byMedia[record.MediaID]; seen {
			continue
		}
		byMedia[record.MediaID] = record
		mediaIDs = append(mediaIDs, record.MediaID)
	}
	if len(mediaIDs) == 0 {
		c.Status(http.StatusOK)
		return
	}

	location := mediaidentity.AiringLocation(handler.timeZone)
	today := mediaidentity.AiringDay(handler.now(), location)
	units, err := handler.todayUpdateReader.ListDailyUpdatesForMedia(c.Request.Context(), mediaIDs, today)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("today updates: load air units failed",
			"user_id", userID, "error", err)
		c.Status(http.StatusOK)
		return
	}
	if len(units) == 0 {
		c.Status(http.StatusOK)
		return
	}
	// 取当天最早的一集。TMDB 偶尔会把整段待定档剧集都标成同一天，
	// 取最大集号会把尚未更新的集数当成“今日更新”。
	firstByMedia := make(map[int]mediaidentity.MediaUnit, len(units))
	order := make([]int, 0, len(units))
	for _, unit := range units {
		if _, seen := firstByMedia[unit.MediaID]; seen {
			continue
		}
		order = append(order, unit.MediaID)
		firstByMedia[unit.MediaID] = unit
	}

	updates := make([]TodayUpdate, 0, len(order))
	for _, mediaID := range order {
		record, ok := byMedia[mediaID]
		if !ok {
			continue
		}
		unit := firstByMedia[mediaID]
		episodeKey := unit.EpisodeKey
		if episodeKey == "" {
			episodeKey = mediaidentity.EpisodeLabel(unit.SeasonNumber, unit.EpisodeNumber)
		}
		var playable bool
		if handler.episodeReader != nil {
			candidates, candidateErr := handler.episodeReader.ListResourceCandidates(c.Request.Context(), mediaID, unit.SeasonNumber, episodeKey)
			if candidateErr != nil {
				requestmeta.Logger(c.Request.Context()).Warn("today updates: load episode candidates failed",
					"user_id", userID, "media_id", mediaID, "episode_key", episodeKey, "error", candidateErr)
			} else {
				playable = len(candidates) > 0
			}
		}
		updates = append(updates, TodayUpdate{
			MediaID:       mediaID,
			DoubanID:      record.DoubanID,
			Title:         record.Title,
			Poster:        record.Poster,
			EpisodeLabel:  mediaidentity.EpisodeLabel(unit.SeasonNumber, unit.EpisodeNumber),
			EpisodeTitle:  unit.Title,
			WatchingLabel: record.Episode,
			ContinueURL:   historyContinueURL(record),
			Playable:      playable,
		})
		if len(updates) >= todayUpdatesLimit {
			break
		}
	}
	if len(updates) == 0 {
		c.Status(http.StatusOK)
		return
	}
	c.HTML(http.StatusOK, "partials/today_updates.html", gin.H{"Updates": updates})
}

// historyContinueURL 复用观看记录的入口、集数和优选资源，今日更新只负责提醒。
func historyContinueURL(record Record) string {
	sourceKey, vodID := record.Source, record.VodID
	if record.PreferredSource != "" && record.PreferredVodID != "" {
		sourceKey, vodID = record.PreferredSource, record.PreferredVodID
	}
	if record.EntryPage == "watch" && record.DoubanID != "" {
		return fmt.Sprintf("/watch/%s?ep=%s&source_key=%s&vod_id=%s", url.PathEscape(record.DoubanID), url.QueryEscape(record.Episode), url.QueryEscape(sourceKey), url.QueryEscape(vodID))
	}
	if sourceKey != "" && vodID != "" {
		playURL := fmt.Sprintf("/play/%s/%s?ep=%s", url.PathEscape(sourceKey), url.PathEscape(vodID), url.QueryEscape(record.Episode))
		if record.DoubanID != "" {
			playURL += "&douban_id=" + url.QueryEscape(record.DoubanID)
		}
		return playURL
	}
	return "/search?kw=" + url.QueryEscape(record.Title)
}
