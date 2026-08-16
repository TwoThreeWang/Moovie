package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

func TestGenerateFreezesMonthlyStatsPersonaPercentileAndPosterWall(t *testing.T) {
	reports := NewMemoryStore()
	libraryStore := library.NewMemoryStore()
	catalogStore := catalog.NewMemoryStore()
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
	counts := map[int]int{7: 5, 8: 1, 9: 2, 10: 5, 11: 6}
	if err := service.Generate(t.Context(), 7, "2026-07", counts); err != nil {
		t.Fatal(err)
	}
	report, _ := reports.GetByUserAndMonth(t.Context(), 7, "2026-07")
	if report == nil || report.Status != StatusGenerated || report.WatchedCount != 5 || report.AvgRating != 4 || report.TopMovieID != "1" || report.ContinuousDays != 2 || report.PersonaTitle != "深夜悬疑侦探" || report.PercentileRank != 50 || report.FeaturedQuote != "最喜欢的一部" {
		t.Fatalf("generated report = %+v", report)
	}
	if !strings.Contains(report.PersonaLine, "40%") || !strings.Contains(report.PersonaLine, "凌晨 1 点") {
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
	reports := NewMemoryStore()
	service := NewService(reports, library.NewMemoryStore(), catalog.NewMemoryStore())
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
