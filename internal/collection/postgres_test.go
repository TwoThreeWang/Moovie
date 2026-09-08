package collection

import (
	"errors"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

// TestCollectionSaveAndReadRunAgainstPostgres 让片单的整套读写在真库上跑一遍：
// 事务替换条目、认不出的豆瓣 ID 要报回来、空片单和草稿不能进公开列表。
func TestCollectionSaveAndReadRunAgainstPostgres(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Media(t, pool, 1, 2, 3)
	store := NewPostgresStore(pool)
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `UPDATE media SET douban_id = 'd' || id, title = 'Film ' || id, poster = 'p' || id`); err != nil {
		t.Fatalf("准备影片: %v", err)
	}

	id, unknown, err := store.Save(ctx, Collection{Slug: "best-2024", Title: "2024 最佳", Description: "选片说明", Featured: true},
		[]ItemInput{{DoubanID: "d1", Note: "第一部"}, {DoubanID: "不存在", Note: "会被跳过"}, {DoubanID: "d2", Note: "第二部"}})
	if err != nil || id == 0 {
		t.Fatalf("Save() = %d/%v", id, err)
	}
	// 粘错的 ID 必须报回来：静默丢弃是策展时最难发现的错误。
	if len(unknown) != 1 || unknown[0] != "不存在" {
		t.Fatalf("unknown = %#v", unknown)
	}

	saved, err := store.GetBySlug(ctx, "best-2024")
	if err != nil || saved == nil || saved.ItemCount != 2 || !saved.Official() || len(saved.Covers) != 2 {
		t.Fatalf("GetBySlug() = %+v/%v", saved, err)
	}

	items, err := store.ListItems(ctx, id)
	if err != nil || len(items) != 2 {
		t.Fatalf("ListItems() = %#v/%v", items, err)
	}
	// 跳过的那条不能占掉序号，位置要连续。
	if items[0].DoubanID != "d1" || items[0].Position != 1 || items[1].DoubanID != "d2" || items[1].Position != 2 {
		t.Fatalf("条目顺序 = %#v", items)
	}

	// 再保存一次是整体替换，不是追加。
	if _, _, err := store.Save(ctx, Collection{ID: id, Slug: "best-2024", Title: "2024 最佳", Featured: true, Description: "选片说明"},
		[]ItemInput{{DoubanID: "d3", Note: "换掉了"}}); err != nil {
		t.Fatalf("二次保存: %v", err)
	}
	if items, _ := store.ListItems(ctx, id); len(items) != 1 || items[0].DoubanID != "d3" {
		t.Fatalf("替换后条目 = %#v", items)
	}

	// 草稿和空片单都不能进公开列表与 sitemap。
	draftID, _, err := store.Save(ctx, Collection{Slug: "draft", Title: "还没发"}, []ItemInput{{DoubanID: "d1"}})
	if err != nil {
		t.Fatalf("保存草稿: %v", err)
	}
	if _, _, err := store.Save(ctx, Collection{Slug: "empty", Title: "空的"}, nil); err != nil {
		t.Fatalf("保存空片单: %v", err)
	}
	featured, err := store.ListFeatured(ctx, 20, 0)
	if err != nil || len(featured) != 1 || featured[0].Slug != "best-2024" {
		t.Fatalf("ListFeatured() = %#v/%v", featured, err)
	}
	if count, _ := store.CountFeatured(ctx); count != 1 {
		t.Fatalf("CountFeatured() = %d", count)
	}
	if slugs, _ := store.FeaturedForSitemap(ctx); len(slugs) != 1 || slugs[0].Slug != "best-2024" {
		t.Fatalf("FeaturedForSitemap() = %#v", slugs)
	}
	// 后台列表要看得见草稿。
	if all, _ := store.ListAll(ctx, 20, 0); len(all) != 3 {
		t.Fatalf("ListAll() = %d 条", len(all))
	}

	if err := store.Delete(ctx, draftID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if gone, _ := store.GetBySlug(ctx, "draft"); gone != nil {
		t.Fatalf("删除后仍能取到 = %+v", gone)
	}
}

func TestEmptyPublicationRollsBackCollectionAndItems(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Media(t, pool, 1)
	if _, err := pool.Exec(t.Context(), `UPDATE media SET douban_id = '1292052' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(pool)
	id, _, err := store.Save(t.Context(), Collection{Slug: "kept", Title: "原片单", Featured: true, Description: "选片说明"}, []ItemInput{{DoubanID: "1292052", Note: "原推荐语"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []Collection{{Slug: "new-empty", Title: "新片单", Featured: true, Description: "选片说明"}, {ID: id, Slug: "changed", Title: "不应保存", Featured: true, Description: "选片说明"}} {
		_, unknown, err := store.Save(t.Context(), candidate, []ItemInput{{DoubanID: "missing", Note: "推荐理由"}})
		if !errors.Is(err, ErrEmptyPublished) || len(unknown) != 1 {
			t.Fatalf("empty publication = %v/%v", unknown, err)
		}
		if leaked, err := store.GetBySlug(t.Context(), candidate.Slug); err != nil || leaked != nil {
			t.Fatalf("failed save persisted: %+v/%v", leaked, err)
		}
	}
	kept, err := store.GetBySlug(t.Context(), "kept")
	if err != nil || kept == nil || kept.Title != "原片单" || kept.ItemCount != 1 {
		t.Fatalf("original changed: %+v/%v", kept, err)
	}
	items, err := store.ListItems(t.Context(), id)
	if err != nil || len(items) != 1 || items[0].Note != "原推荐语" {
		t.Fatalf("original items changed: %+v/%v", items, err)
	}
	if _, _, err := store.Save(t.Context(), Collection{Slug: "empty-draft", Title: "草稿"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionTemplatesCompile(t *testing.T) {
	// 三张新页面加进了 contentPages，编译不过启动时才炸就太晚了。
	if err := compileCollectionTemplates(); err != nil {
		t.Fatalf("片单模板解析失败: %v", err)
	}
}
