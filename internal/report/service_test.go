package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestGenerateFreezesMonthlyStatsPersonaPercentileAndPosterWall(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	reports := NewPostgresStore(testdb.Pool(t))
	libraryStore := library.NewPostgresStore(testdb.Pool(t))
	catalogStore := catalog.NewPostgresStore(testdb.Pool(t))
	location := time.Local
	records := []library.Record{
		{MovieID: "1", Title: "最佳", Poster: "p1", Rating: 5, Comment: "最喜欢的一部", CreatedAt: time.Date(2026, 7, 31, 23, 0, 0, 0, location)},
		{MovieID: "2", Title: "二", Poster: "p2", Rating: 4, CreatedAt: time.Date(2026, 7, 30, 1, 0, 0, 0, location)},
		{MovieID: "3", Title: "三", Poster: "p3", Rating: 3, CreatedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, location)},
		{MovieID: "4", Title: "四", Poster: "p4", CreatedAt: time.Date(2026, 7, 10, 12, 0, 0, 0, location)},
		{MovieID: "5", Title: "五", Poster: "p5", CreatedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, location)},
	}
	for _, record := range records {
		record.UserID, record.Status, record.UpdatedAt = 7, library.StatusWatched, record.CreatedAt
		_ = libraryStore.Upsert(t.Context(), record)
		_ = catalogStore.Upsert(t.Context(), catalog.Movie{DoubanID: record.MovieID, Title: record.Title, Genres: "悬疑,犯罪"})
	}
	service := NewService(reports, libraryStore, catalogStore)
	for i := 0; i < 10; i++ {
		stats := service.calculateStats(t.Context(), records).GenreStats
		if len(stats) != 2 || stats[0].Genre != "悬疑" || stats[1].Genre != "犯罪" {
			t.Fatalf("unstable tied genres: %+v", stats)
		}
	}
	counts := map[int]int{7: 5, 8: 1, 9: 2, 10: 5, 11: 6}
	if err := service.Generate(t.Context(), 7, "2026-07", counts); err != nil {
		t.Fatal(err)
	}
	report, _ := reports.GetByUserAndMonth(t.Context(), 7, "2026-07")
	if report == nil || report.Status != StatusGenerated || report.WatchedCount != 5 || report.AvgRating != 4 || report.TopMovieID != "1" || report.ContinuousDays != 2 || report.PersonaTitle != "悬疑侦探" || report.PercentileRank != 50 || report.FeaturedQuote != "最喜欢的一部" {
		t.Fatalf("generated report = %+v", report)
	}
	if report.PersonaLine != "本月片单中，你评分最高的作品之一是《最佳》。" {
		t.Fatalf("persona line = %q", report.PersonaLine)
	}
	var wall []PosterWallItem
	if json.Unmarshal([]byte(report.PosterWall), &wall) != nil || len(wall) != 4 {
		t.Fatalf("poster wall = %q", report.PosterWall)
	}
	for _, item := range wall {
		if item.MovieID == "1" {
			t.Fatalf("top movie was not excluded from five-item wall: %+v", wall)
		}
	}
}

func TestGenerateRejectsInvalidOrEmptyMonthAndPersistsFailure(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	reports := NewPostgresStore(testdb.Pool(t))
	service := NewService(reports, library.NewPostgresStore(testdb.Pool(t)), catalog.NewPostgresStore(testdb.Pool(t)))
	if err := service.Generate(t.Context(), 7, "2026-13", nil); err == nil {
		t.Fatal("invalid month was accepted")
	}
	err := service.Generate(t.Context(), 7, "2026-07", nil)
	if err == nil || !strings.Contains(err.Error(), "本月无观影记录") {
		t.Fatalf("empty month error = %v", err)
	}
	report, _ := reports.GetByUserAndMonth(t.Context(), 7, "2026-07")
	if report == nil || report.Status != StatusFailed || report.ErrorMessage != "本月无观影记录" {
		t.Fatalf("failed report = %+v", report)
	}
}

func TestPersonaEvidenceThresholds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		stats []GenreStat
		want  string
	}{
		{"single movie", 1, []GenreStat{{Genre: "爱情", Count: 1}, {Genre: "剧情", Count: 1}}, "观影小记"},
		{"missing metadata", 5, nil, "观影小记"},
		{"weak second genre", 11, []GenreStat{{Genre: "悬疑", Count: 10}, {Genre: "犯罪", Count: 1}}, "推理爱好者"},
		{"weak relative genre", 10, []GenreStat{{Genre: "悬疑", Count: 10}, {Genre: "犯罪", Count: 2}}, "推理爱好者"},
		{"supported combo", 4, []GenreStat{{Genre: "悬疑", Count: 4}, {Genre: "犯罪", Count: 2}}, "悬疑侦探"},
		{"diverse", 7, []GenreStat{{Genre: "动作", Count: 3}, {Genre: "喜剧", Count: 2}}, "多元探索者"},
		{"drama", 4, []GenreStat{{Genre: "剧情", Count: 4}}, "故事爱好者"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := personaTitle(tc.stats, tc.count); got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestContinuousDaysUsesLongestMonthlyRunAndLocalDates(t *testing.T) {
	var records []library.Record
	// Deliberately unordered, repeated dates, mid-month streak and midnight UTC representation.
	for _, day := range []int{31, 12, 10, 11, 11, 20} {
		records = append(records, library.Record{CreatedAt: time.Date(2026, 7, day, 0, 0, 0, 0, time.Local).UTC()})
	}
	if got := continuousDays(records); got != 3 {
		t.Fatalf("longest run = %d, want 3", got)
	}
	if got := continuousDays(nil); got != 0 {
		t.Fatalf("empty run = %d", got)
	}
}

func TestPersonaCopyDoesNotInferViewingTimeOrSurprise(t *testing.T) {
	for _, count := range []int{1, 5, 20} {
		data := reportData{WatchedCount: count, GenreKnownCount: count, GenreStats: []GenreStat{{Genre: "剧情", Count: count}}, TopMovie: &TopMovie{Title: "作品"}}
		_, line := buildPersona(data)
		if strings.Contains(line, "深夜") || strings.Contains(line, "惊喜") || strings.Contains(line, "刷完") {
			t.Fatalf("unsupported inference: %s", line)
		}
	}
	_, line := buildPersona(reportData{WatchedCount: 5, GenreKnownCount: 5, ContinuousDays: 5, GenreStats: []GenreStat{{Genre: "剧情", Count: 5}}})
	if line != "本月最长连续 5 天都有片单记录。" {
		t.Fatal(line)
	}
}

func TestSplitGenresTrimsAndCountsEachGenreOnce(t *testing.T) {
	got := splitGenres(" 剧情 / 爱情,剧情、 , 爱情 ")
	if strings.Join(got, ",") != "剧情,爱情" {
		t.Fatalf("genres = %v", got)
	}
}

func TestPersonaMissingGenresDoNotImplyDiversity(t *testing.T) {
	title, line := buildPersona(reportData{WatchedCount: 10, GenreKnownCount: 1, GenreStats: []GenreStat{{Genre: "剧情", Count: 1}}})
	if title != "观影小记" || !strings.Contains(line, "资料不足") {
		t.Fatalf("%s: %s", title, line)
	}
}
