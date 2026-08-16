package douban

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

func TestFullSyncFiltersNonVideoAndPreservesLocalRatingComment(t *testing.T) {
	provider := &fakeInterestProvider{pages: map[string][]Interest{
		"movie/mark": {
			interest("100", "movie", "", "mark", "电影", "2026-07-01 12:00:00"),
			interest("200", "book", "", "mark", "图书", "2026-07-02 12:00:00"),
		},
		"movie/done": {interest("300", "", "show", "done", "综艺", "2026-07-03")},
		"tv/done":    {interest("400", "tv", "", "done", "剧集", "2026-07-04")},
	}}
	libraryStore := library.NewMemoryStore()
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: 7, MovieID: "100", Status: library.StatusWish, Rating: 5, Comment: "本地短评"})
	jobs := NewMemoryJobStore()
	job, _ := jobs.Create(t.Context(), 7, TypeFull)
	service := NewService(provider, libraryStore, jobs, WithPageDelay(func(context.Context) error { return nil }))
	if err := service.SyncFull(t.Context(), 7, "douban-user", job.ID); err != nil {
		t.Fatal(err)
	}

	record, _ := libraryStore.GetByUserAndMovie(t.Context(), 7, "100")
	if record == nil || record.Rating != 5 || record.Comment != "本地短评" || record.Status != library.StatusWish {
		t.Fatalf("preserved record = %+v", record)
	}
	if skipped, _ := libraryStore.GetByUserAndMovie(t.Context(), 7, "200"); skipped != nil {
		t.Fatalf("book was persisted: %+v", skipped)
	}
	show, _ := libraryStore.GetByUserAndMovie(t.Context(), 7, "300")
	tv, _ := libraryStore.GetByUserAndMovie(t.Context(), 7, "400")
	if show == nil || tv == nil || show.Status != library.StatusWatched || tv.Status != library.StatusWatched {
		t.Fatalf("show/tv = %+v/%+v", show, tv)
	}
	latest, _ := jobs.LatestByUser(t.Context(), 7)
	if latest.Total != 4 || latest.Processed != 3 || latest.FailedCount != 0 {
		t.Fatalf("job progress = %+v", latest)
	}
}

func TestIncrementalSyncUsesRSSSetAndStopsAtEarliestBoundary(t *testing.T) {
	earliest := time.Date(2026, time.July, 2, 0, 0, 0, 0, time.Local)
	provider := &fakeInterestProvider{
		pages: map[string][]Interest{"movie/mark": {
			interest("100", "movie", "", "mark", "应同步", "2026-07-03 12:00:00"),
			interest("101", "movie", "", "mark", "不在RSS", "2026-07-03 11:00:00"),
			interest("102", "movie", "", "mark", "越过边界", "2026-06-30 12:00:00"),
		}},
		rssSubjects: map[string]bool{"100": true, "102": true}, rssEarliest: earliest,
	}
	libraryStore := library.NewMemoryStore()
	jobs := NewMemoryJobStore()
	job, _ := jobs.Create(t.Context(), 7, TypeIncremental)
	service := NewService(provider, libraryStore, jobs, WithPageDelay(func(context.Context) error { return nil }))
	if err := service.SyncIncremental(t.Context(), 7, "douban-user", job.ID); err != nil {
		t.Fatal(err)
	}
	if record, _ := libraryStore.GetByUserAndMovie(t.Context(), 7, "100"); record == nil {
		t.Fatal("RSS subject was not synchronized")
	}
	for _, id := range []string{"101", "102"} {
		if record, _ := libraryStore.GetByUserAndMovie(t.Context(), 7, id); record != nil {
			t.Fatalf("unexpected incremental record %s: %+v", id, record)
		}
	}
	latest, _ := jobs.LatestByUser(t.Context(), 7)
	if latest.Processed != 1 || latest.Cursor != "" {
		t.Fatalf("incremental progress = %+v", latest)
	}
}

type fakeInterestProvider struct {
	pages       map[string][]Interest
	rssSubjects map[string]bool
	rssEarliest time.Time
}

func (provider *fakeInterestProvider) ValidateUser(context.Context, string) error { return nil }

func (provider *fakeInterestProvider) Interests(_ context.Context, _, itemType, status string, start, _ int) ([]Interest, int, error) {
	items := provider.pages[itemType+"/"+status]
	if start > 0 {
		return []Interest{}, len(items), nil
	}
	return append([]Interest(nil), items...), len(items), nil
}

func (provider *fakeInterestProvider) RSSSubjects(context.Context, string) (map[string]bool, time.Time, error) {
	return provider.rssSubjects, provider.rssEarliest, nil
}

func interest(id, kind, subtype, status, title, createdAt string) Interest {
	return Interest{Status: status, CreateTime: createdAt, Subject: Subject{ID: json.Number(id), Type: kind, Subtype: subtype, Title: title, Year: "2026", CoverURL: "poster-" + id}}
}
