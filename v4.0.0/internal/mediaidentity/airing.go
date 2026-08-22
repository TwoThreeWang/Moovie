package mediaidentity

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SeriesEnded 判断一部作品是否已经不会再更新。
//
// 这里刻意采用"白名单式的完结判定"：只有明确是完结/砍剧状态才返回 true。
// 空串（未同步过 TMDB）和未知的新状态值都按"可能还会更新"处理——
// 追剧场景下漏掉一次更新提醒，比错误地把在播剧标成完结更让人困扰。
func SeriesEnded(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ended", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

// AiringLocation 是判定"今天"和格式化播出日期使用的时区。
// air_date 在库中是 DATE，本身不带时区；用户看到的"今天更新"必须按本地日历判断，
// 否则 UTC 与东八区之间会出现整整一天的偏差。
func AiringLocation(name string) *time.Location {
	if location, err := time.LoadLocation(strings.TrimSpace(name)); err == nil && location != nil {
		return location
	}
	// 容器缺少 tzdata 时 LoadLocation 会失败。固定东八区偏移比退回 UTC 更接近预期。
	return time.FixedZone("CST", 8*60*60)
}

// AiringDay 把任意时刻收敛成给定时区下的当天零点，用于和 DATE 类型的 air_date 比较。
func AiringDay(moment time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.UTC
	}
	local := moment.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}

// EpisodeLabel 输出 S01E05 形式的集次标识。
func EpisodeLabel(seasonNumber, episodeNumber int) string {
	if seasonNumber <= 0 {
		seasonNumber = 1
	}
	if episodeNumber <= 0 {
		return fmt.Sprintf("S%02d", seasonNumber)
	}
	return fmt.Sprintf("S%02dE%02d", seasonNumber, episodeNumber)
}

// airingWeekdays 下标对应 time.Weekday。
var airingWeekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// AiringEpisodeView 是模板直接渲染的单集播出信息。
type AiringEpisodeView struct {
	EpisodeLabel string // S02E07
	Title        string
	AirDateISO   string // 2026-08-20，用于 <time datetime>
	AirDateText  string // 8月20日；跨年时带上年份
	WeekdayText  string // 周三
	RelativeText string // 今天 / 明天 / 后天 / N天后
	DaysUntil    int
}

// AirScheduleView 是详情页和播放页"更新时间"区块的数据。
// Show 为 false 时模板整块不渲染——已完结、非剧集、无已知日期三种情况都不该占版面。
type AirScheduleView struct {
	Show     bool
	Next     AiringEpisodeView
	Upcoming []AiringEpisodeView
}

// BuildAirScheduleView 把未播出剧集列表整理成可直接渲染的视图。
//
// units 必须已经按播出日期升序，且只包含 air_date 不为空、且不早于今天的集次
// （ListUpcomingUnits 已保证这两点）。
func BuildAirScheduleView(seriesStatus string, units []MediaUnit, now time.Time, location *time.Location) AirScheduleView {
	if SeriesEnded(seriesStatus) || len(units) == 0 {
		return AirScheduleView{}
	}
	if location == nil {
		location = time.UTC
	}
	today := AiringDay(now, location)
	views := make([]AiringEpisodeView, 0, len(units))
	for _, unit := range units {
		if unit.AirDate.IsZero() {
			continue
		}
		views = append(views, buildAiringEpisodeView(unit, today, location))
	}
	if len(views) == 0 {
		return AirScheduleView{}
	}
	return AirScheduleView{Show: true, Next: views[0], Upcoming: views}
}

// buildAiringEpisodeView 把一条季集记录整理成模板可直接渲染的播出信息。
func buildAiringEpisodeView(unit MediaUnit, today time.Time, location *time.Location) AiringEpisodeView {
	// air_date 是不带时区的 DATE，驱动会把它解析成 UTC 零点。
	// 这里只取年月日再按展示时区重建，避免时区换算把日期整体挪动一天。
	airDay := time.Date(unit.AirDate.Year(), unit.AirDate.Month(), unit.AirDate.Day(), 0, 0, 0, 0, location)
	// 加半天再整除，等价于四舍五入到最近的整天。
	// 直接除会在存在夏令时的时区把 167 小时截断成 6 天。
	daysUntil := int((airDay.Sub(today) + 12*time.Hour) / (24 * time.Hour))
	dateText := fmt.Sprintf("%d月%d日", int(airDay.Month()), airDay.Day())
	if airDay.Year() != today.Year() {
		dateText = fmt.Sprintf("%d年%s", airDay.Year(), dateText)
	}
	return AiringEpisodeView{
		EpisodeLabel: EpisodeLabel(unit.SeasonNumber, unit.EpisodeNumber),
		Title:        unit.Title,
		AirDateISO:   airDay.Format("2006-01-02"),
		AirDateText:  dateText,
		WeekdayText:  airingWeekdays[int(airDay.Weekday())],
		RelativeText: relativeDayText(daysUntil),
		DaysUntil:    daysUntil,
	}
}

// relativeDayText 把天数差转成「今天/明天/后天/N天后」。
func relativeDayText(daysUntil int) string {
	switch {
	case daysUntil <= 0:
		return "今天"
	case daysUntil == 1:
		return "明天"
	case daysUntil == 2:
		return "后天"
	default:
		return fmt.Sprintf("%d天后", daysUntil)
	}
}

// ListUpcomingUnits 返回某部作品在 from（含）之后播出的剧集，按播出日期升序。
//
// 只查 unit_type = 'episode'：特别篇和预告不属于"下一集什么时候更新"的语义。
// air_date IS NULL 的集次被排除——TMDB 对未定档集次经常返回 null，
// 把它们当成已知日期展示会得到 0001-01-01 这样的假数据。
func (store *PostgresStore) ListUpcomingUnits(ctx context.Context, mediaID, seasonNumber int, from time.Time, limit int) ([]MediaUnit, error) {
	if mediaID <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 12
	}
	rows, err := store.database.Query(ctx, `SELECT id, media_id, unit_type, season_number,
COALESCE(episode_number, 0), COALESCE(absolute_number, 0), episode_key, title, air_date,
COALESCE(runtime_minutes, 0)
FROM media_units
WHERE media_id = $1 AND ($2 <= 0 OR season_number = $2)
  AND unit_type = 'episode' AND air_date IS NOT NULL AND air_date >= $3::date
ORDER BY air_date ASC, season_number ASC, episode_number ASC
LIMIT $4`, mediaID, seasonNumber, from, limit)
	if err != nil {
		return nil, fmt.Errorf("list upcoming units: %w", err)
	}
	defer rows.Close()
	units := make([]MediaUnit, 0, limit)
	for rows.Next() {
		var unit MediaUnit
		var airDate *time.Time
		if err := rows.Scan(&unit.ID, &unit.MediaID, &unit.UnitType, &unit.SeasonNumber,
			&unit.EpisodeNumber, &unit.AbsoluteNumber, &unit.EpisodeKey, &unit.Title,
			&airDate, &unit.RuntimeMinutes); err != nil {
			return nil, fmt.Errorf("scan upcoming unit: %w", err)
		}
		if airDate != nil {
			unit.AirDate = *airDate
		}
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upcoming units: %w", err)
	}
	return units, nil
}

// ListDailyUpdatesForMedia 返回给定媒体集合中在 day 当天播出的剧集。
// 首页"今日更新"先由调用方收敛出用户在看的 media_id，再用这里取播出集次；
// 把用户维度的查询留在 history 侧，可以避免 mediaidentity 反向依赖用户数据。
func (store *PostgresStore) ListDailyUpdatesForMedia(ctx context.Context, mediaIDs []int, day time.Time) ([]MediaUnit, error) {
	if len(mediaIDs) == 0 {
		return nil, nil
	}
	// media.id 是 BIGSERIAL；显式转成 int64 并在 SQL 侧标注 bigint[]，
	// 避免依赖驱动对 Go int 的默认宽度推断。
	ids := make([]int64, 0, len(mediaIDs))
	for _, mediaID := range mediaIDs {
		if mediaID > 0 {
			ids = append(ids, int64(mediaID))
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := store.database.Query(ctx, `SELECT id, media_id, unit_type, season_number,
COALESCE(episode_number, 0), COALESCE(absolute_number, 0), episode_key, title, air_date,
COALESCE(runtime_minutes, 0)
FROM media_units
WHERE unit_type = 'episode' AND air_date = $2::date AND media_id = ANY($1::bigint[])
ORDER BY media_id ASC, season_number ASC, episode_number ASC`, ids, day)
	if err != nil {
		return nil, fmt.Errorf("list daily updates: %w", err)
	}
	defer rows.Close()
	units := make([]MediaUnit, 0, len(mediaIDs))
	for rows.Next() {
		var unit MediaUnit
		var airDate *time.Time
		if err := rows.Scan(&unit.ID, &unit.MediaID, &unit.UnitType, &unit.SeasonNumber,
			&unit.EpisodeNumber, &unit.AbsoluteNumber, &unit.EpisodeKey, &unit.Title,
			&airDate, &unit.RuntimeMinutes); err != nil {
			return nil, fmt.Errorf("scan daily update: %w", err)
		}
		if airDate != nil {
			unit.AirDate = *airDate
		}
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily updates: %w", err)
	}
	return units, nil
}
