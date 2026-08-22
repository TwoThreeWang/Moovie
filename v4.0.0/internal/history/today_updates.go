package history

import (
	"context"
	"net/http"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
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
	Playback      search.PlaybackSummary
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
	playback := make(map[int]search.PlaybackSummary)
	if handler.playbackReader != nil {
		if summaries, summaryErr := handler.playbackReader.ListPlaybackSummaries(c.Request.Context(), mediaIDs); summaryErr == nil {
			playback = summaries
		} else {
			requestmeta.Logger(c.Request.Context()).Warn("today updates: load playback summaries failed",
				"user_id", userID, "error", summaryErr)
		}
	}

	// 一部剧同一天可能连更多集，这里只保留集号最大的一集作为"最新更新"。
	// units 已按 media_id、season、episode 升序，因此后来的覆盖先前的即可。
	latest := make(map[int]mediaidentity.MediaUnit, len(units))
	order := make([]int, 0, len(units))
	for _, unit := range units {
		if _, seen := latest[unit.MediaID]; !seen {
			order = append(order, unit.MediaID)
		}
		latest[unit.MediaID] = unit
	}

	updates := make([]TodayUpdate, 0, len(order))
	for _, mediaID := range order {
		record, ok := byMedia[mediaID]
		if !ok {
			continue
		}
		unit := latest[mediaID]
		updates = append(updates, TodayUpdate{
			MediaID:       mediaID,
			DoubanID:      record.DoubanID,
			Title:         record.Title,
			Poster:        record.Poster,
			EpisodeLabel:  mediaidentity.EpisodeLabel(unit.SeasonNumber, unit.EpisodeNumber),
			EpisodeTitle:  unit.Title,
			WatchingLabel: record.Episode,
			Playback:      playback[mediaID],
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
