package mediaidentity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestSeriesEndedOnlyTreatsExplicitFinalStatesAsEnded(t *testing.T) {
	for _, status := range []string{"Ended", "ended", " Canceled ", "Cancelled"} {
		if !SeriesEnded(status) {
			t.Fatalf("SeriesEnded(%q) = false, want true", status)
		}
	}
	// 未知和空状态必须按"可能还会更新"处理：
	// 漏掉一次更新提醒，好过把在播剧错误地标成完结。
	for _, status := range []string{"", "Returning Series", "In Production", "Planned", "Pilot"} {
		if SeriesEnded(status) {
			t.Fatalf("SeriesEnded(%q) = true, want false", status)
		}
	}
}

func TestAiringDayUsesLocalCalendarNotUTC(t *testing.T) {
	shanghai := AiringLocation("Asia/Shanghai")
	// UTC 的 8 月 13 日 23:00 已经是东八区的 8 月 14 日。
	// 按 UTC 判断"今天"会让首页整整晚一天显示更新。
	moment := time.Date(2026, time.August, 13, 23, 0, 0, 0, time.UTC)
	day := AiringDay(moment, shanghai)
	if day.Year() != 2026 || day.Month() != time.August || day.Day() != 14 {
		t.Fatalf("AiringDay = %s, want 2026-08-14 local", day)
	}
	if hour, minute := day.Hour(), day.Minute(); hour != 0 || minute != 0 {
		t.Fatalf("AiringDay is not midnight: %s", day)
	}
}

func TestAiringLocationFallsBackWhenTZDataMissing(t *testing.T) {
	location := AiringLocation("Definitely/NotAZone")
	if location == nil {
		t.Fatal("AiringLocation returned nil")
	}
	_, offset := time.Date(2026, time.August, 14, 0, 0, 0, 0, location).Zone()
	if offset != 8*60*60 {
		t.Fatalf("fallback offset = %d, want 28800", offset)
	}
}

func TestListUpcomingUnitsLimitsSeasonSpecificMedia(t *testing.T) {
	executor := &airingQueryExecutor{}
	store := NewPostgresStore(executor)
	from := time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC)
	units, err := store.ListUpcomingUnits(t.Context(), 3802, 1, from, 8)
	if err != nil || len(units) != 0 {
		t.Fatalf("units/error = %+v/%v", units, err)
	}
	if !strings.Contains(executor.query, "($2 <= 0 OR season_number = $2)") {
		t.Fatalf("query does not scope season: %s", executor.query)
	}
	if !reflect.DeepEqual(executor.arguments, []any{3802, 1, from, 8}) {
		t.Fatalf("arguments = %#v", executor.arguments)
	}
}

type airingQueryExecutor struct {
	query     string
	arguments []any
}

func (executor *airingQueryExecutor) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	executor.query, executor.arguments = query, arguments
	return emptyAiringRows{}, nil
}

func (*airingQueryExecutor) QueryRow(context.Context, string, ...any) database.Row { return nil }
func (*airingQueryExecutor) Exec(context.Context, string, ...any) (int64, error) {
	return 0, errors.New("unexpected Exec")
}

type emptyAiringRows struct{}

func (emptyAiringRows) Next() bool        { return false }
func (emptyAiringRows) Scan(...any) error { return errors.New("unexpected Scan") }
func (emptyAiringRows) Err() error        { return nil }
func (emptyAiringRows) Close()            {}

func TestEpisodeLabelFormatsSeasonAndEpisode(t *testing.T) {
	if got := EpisodeLabel(2, 7); got != "S02E07" {
		t.Fatalf("EpisodeLabel(2,7) = %q", got)
	}
	if got := EpisodeLabel(0, 3); got != "S01E03" {
		t.Fatalf("EpisodeLabel(0,3) = %q, want season defaulted to 1", got)
	}
	if got := EpisodeLabel(1, 0); got != "S01" {
		t.Fatalf("EpisodeLabel(1,0) = %q, want season-only label", got)
	}
}

func TestBuildAirScheduleViewHidesEndedAndEmptySeries(t *testing.T) {
	shanghai := AiringLocation("Asia/Shanghai")
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, shanghai)
	units := []MediaUnit{{
		MediaID: 1, UnitType: "episode", SeasonNumber: 2, EpisodeNumber: 7,
		AirDate: time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC),
	}}

	if view := BuildAirScheduleView("Ended", units, now, shanghai); view.Show {
		t.Fatal("ended series must not show an air schedule")
	}
	if view := BuildAirScheduleView("Returning Series", nil, now, shanghai); view.Show {
		t.Fatal("series without known air dates must not show an air schedule")
	}
}

func TestBuildAirScheduleViewFormatsNextEpisode(t *testing.T) {
	shanghai := AiringLocation("Asia/Shanghai")
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, shanghai)
	units := []MediaUnit{
		{
			MediaID: 1, UnitType: "episode", SeasonNumber: 2, EpisodeNumber: 7, Title: "第七集",
			// DATE 列由驱动解析成 UTC 零点；视图必须按年月日重建，不做时区换算。
			AirDate: time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			MediaID: 1, UnitType: "episode", SeasonNumber: 2, EpisodeNumber: 8,
			AirDate: time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC),
		},
	}

	view := BuildAirScheduleView("Returning Series", units, now, shanghai)
	if !view.Show || len(view.Upcoming) != 2 {
		t.Fatalf("view = %+v", view)
	}
	next := view.Next
	if next.EpisodeLabel != "S02E07" || next.Title != "第七集" {
		t.Fatalf("next episode = %+v", next)
	}
	if next.AirDateISO != "2026-08-15" || next.AirDateText != "8月15日" {
		t.Fatalf("next date = %q/%q", next.AirDateISO, next.AirDateText)
	}
	if next.WeekdayText != "周六" {
		t.Fatalf("weekday = %q, want 周六", next.WeekdayText)
	}
	if next.DaysUntil != 1 || next.RelativeText != "明天" {
		t.Fatalf("relative = %d/%q", next.DaysUntil, next.RelativeText)
	}
	if view.Upcoming[1].RelativeText != "8天后" {
		t.Fatalf("second episode relative = %q", view.Upcoming[1].RelativeText)
	}
}

func TestBuildAirScheduleViewMarksTodayAndCrossYearDates(t *testing.T) {
	shanghai := AiringLocation("Asia/Shanghai")
	now := time.Date(2026, time.December, 30, 22, 0, 0, 0, shanghai)
	units := []MediaUnit{
		{
			MediaID: 1, UnitType: "episode", SeasonNumber: 1, EpisodeNumber: 5,
			AirDate: time.Date(2026, time.December, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			MediaID: 1, UnitType: "episode", SeasonNumber: 1, EpisodeNumber: 6,
			AirDate: time.Date(2027, time.January, 6, 0, 0, 0, 0, time.UTC),
		},
	}

	view := BuildAirScheduleView("Returning Series", units, now, shanghai)
	if view.Next.RelativeText != "今天" || view.Next.DaysUntil != 0 {
		t.Fatalf("today episode = %+v", view.Next)
	}
	// 跨年时必须带上年份，否则 "1月6日" 会被读成已经过去的日期。
	if view.Upcoming[1].AirDateText != "2027年1月6日" {
		t.Fatalf("cross-year date = %q", view.Upcoming[1].AirDateText)
	}
}
