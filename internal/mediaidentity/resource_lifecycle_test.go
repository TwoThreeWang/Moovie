package mediaidentity

import (
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"testing"
)

func TestResourceMetadataFillsOnlyBlanksAndKeepsProviderPriority(t *testing.T) {
	pool := testdb.Pool(t)
	resources := search.NewPostgresStore(pool)
	identity := NewPostgresStore(pool)
	_, err := resources.CreateSite(t.Context(), search.Site{Key: "a", BaseURL: "https://a.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := search.VodItem{SourceKey: "a", VodId: "42", VodDoubanId: "1292052", VodName: "资源标题", VodPic: "resource-poster", VodContent: "<p>资源简介</p>", TypeName: "电影", VodPlayUrl: "HD$https://video.example/a.m3u8"}
	if err := resources.Upsert(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	initial, err := identity.FindByDoubanID(t.Context(), "1292052")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Title != "资源标题" || initial.Summary != "资源简介" || !initial.LastMetadataSyncAt.IsZero() {
		t.Fatalf("placeholder=%+v", initial)
	}
	var jobs int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM worker_jobs WHERE task_type='douban_metadata' AND subject_key='1292052'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("jobs=%d err=%v", jobs, err)
	}
	if _, err := identity.MergeSource(t.Context(), "douban", Media{DoubanID: "1292052", Title: "豆瓣标题", Poster: "douban-poster", MediaType: "movie"}, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.MergeSource(t.Context(), "manual", Media{DoubanID: "1292052", Poster: "manual-poster"}, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	item.VodName = "资源站新标题"
	item.VodPic = "new-poster"
	item.VodContent = "新的资源简介"
	if err := resources.Upsert(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	got, err := identity.FindByDoubanID(t.Context(), "1292052")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "豆瓣标题" || got.Poster != "manual-poster" || got.Summary != "资源简介" {
		t.Fatalf("protected metadata=%+v", got)
	}
	var provider string
	if err := pool.QueryRow(t.Context(), `SELECT provider FROM media_field_sources WHERE media_id=$1 AND field_name='summary'`, got.ID).Scan(&provider); err != nil || provider != "resource" {
		t.Fatalf("provider=%s err=%v", provider, err)
	}
}
