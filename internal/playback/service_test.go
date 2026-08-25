package playback

import (
	"context"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

type queuedRunner struct {
	tasks []func(context.Context)
}

func (runner *queuedRunner) Run(task func(context.Context)) {
	runner.tasks = append(runner.tasks, task)
}

func TestDetailServiceUsesLocalItemAndQueuesRefresh(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(context.Background(), search.VodItem{SourceKey: "source", VodId: "42", VodName: "local"})
	runner := &queuedRunner{}
	service := NewDetailService(store, store, detailCrawlerFunc(func(context.Context, string, string, string) (*search.VodItem, error) {
		t.Fatal("crawler ran before queued background task")
		return nil, nil
	}), runner, 0)

	item, err := service.Get(context.Background(), "source", "42")
	if err != nil || item == nil || item.VodName != "local" {
		t.Fatalf("item/error = %+v/%v", item, err)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("background tasks = %d", len(runner.tasks))
	}
}

func TestDetailServiceFetchesAndSavesCacheMiss(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	for _, site := range []search.Site{{Key: "source", BaseURL: "https://source.example/api", Enabled: true}} {
		_, _ = store.CreateSite(t.Context(), site)
	}
	service := NewDetailService(store, store, detailCrawlerFunc(func(_ context.Context, baseURL, vodID, sourceKey string) (*search.VodItem, error) {
		if baseURL != "https://source.example/api" || vodID != "42" || sourceKey != "source" {
			t.Fatalf("crawler arguments = %q/%q/%q", baseURL, vodID, sourceKey)
		}
		return &search.VodItem{SourceKey: sourceKey, VodId: vodID, VodName: "remote", VodPlayUrl: "正片$https://video.example/a.m3u8"}, nil
	}), nil, 0)

	item, err := service.Get(context.Background(), "source", "42")
	if err != nil || item == nil || item.VodName != "remote" {
		t.Fatalf("item/error = %+v/%v", item, err)
	}
	stored, _ := store.FindBySourceID(context.Background(), "source", "42")
	if stored == nil || stored.VodName != "remote" {
		t.Fatalf("stored = %+v", stored)
	}
}
