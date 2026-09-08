package collection

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	web "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestCollectionSEOAndEditorialFlow(t *testing.T) {
	pool := testdb.Pool(t)
	store := NewPostgresStore(pool)
	testdb.Media(t, pool, 1)
	if _, err := pool.Exec(t.Context(), `UPDATE media SET douban_id='1292052', title='影片 <测试>' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	renderer, err := web.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"collection", "collections", "admin_collections", "404"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(config.Config{AppSecret: "secret", SiteName: "Moovie", SiteURL: "https://moovie.example", Env: "test"}, store).Register(router)
	token, err := auth.Sign(auth.Claims{UserID: 1, Role: "admin", Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, target string, form url.Values, admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if admin {
			req.AddCookie(&http.Cookie{Name: "token", Value: token})
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	if got := request("GET", "/list", nil, false); got.Code != 200 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/list?page=2", "/list?page=0", "/list?page=-1", "/list?page=x", "/list?page=999999999999999999999"} {
		if got := request("GET", path, nil, false); got.Code != 404 || !strings.Contains(got.Body.String(), "noindex, follow") {
			t.Fatalf("%s: %d", path, got.Code)
		}
	}
	ids := make([]int, 25)
	for i := range ids {
		var err error
		ids[i], _, err = store.Save(t.Context(), Collection{Slug: fmt.Sprintf("list-%d", i), Title: fmt.Sprintf("主题 %d", i), Description: "筛选说明", Featured: true}, []ItemInput{{DoubanID: "1292052", Note: "符合主题的具体原因"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	page := request("GET", "/list?page=2", nil, false)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `rel="canonical" href="https://moovie.example/list?page=2"`) {
		t.Fatal("second page canonical", page.Code)
	}
	if request("GET", "/list?page=3", nil, false).Code != 404 {
		t.Fatal("out of bounds page")
	}
	form := url.Values{"id": {strconv.Itoa(ids[0])}, "slug": {"list-0"}, "title": {"密闭空间 <测试>"}, "description": {"筛选说明"}, "featured": {"on"}, "items": {"1292052 | 原因 </script><script>bad()</script>"}, "related_id_0": {strconv.Itoa(ids[2])}, "related_reason_0": {"继续看封闭环境的人物博弈"}, "related_id_1": {strconv.Itoa(ids[1])}}
	for _, field := range []string{"description", "items"} {
		original := form.Get(field)
		if field == "items" {
			form.Set(field, "1292052 | ")
		} else {
			form.Set(field, "")
		}
		got := request("POST", "/admin/collections", form, true)
		if got.Code != 422 || !strings.Contains(got.Body.String(), "发布前请填写导语") || !strings.Contains(got.Body.String(), "继续看封闭环境的人物博弈") {
			t.Fatalf("validation %s: %d", field, got.Code)
		}
		form.Set(field, original)
	}
	if got := request("POST", "/admin/collections", form, false); got.Code != http.StatusUnauthorized {
		t.Fatal("unauthorized save")
	}
	if got := request("POST", "/admin/collections", form, true); got.Code != 302 {
		t.Fatalf("save: %d %s", got.Code, got.Body.String())
	}
	detail := request("GET", "/list/list-0", nil, false).Body.String()
	if !strings.Contains(detail, "继续看片单") || !strings.Contains(detail, "继续看封闭环境的人物博弈") || strings.Index(detail, `href="/list/list-2"`) > strings.Index(detail, `href="/list/list-1"`) {
		t.Fatal("related display/order")
	}
	blocks := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindAllStringSubmatch(detail, -1)
	if len(blocks) != 2 {
		t.Fatalf("JSON-LD count %d", len(blocks))
	}
	for _, block := range blocks {
		var schema map[string]any
		if err := json.Unmarshal([]byte(block[1]), &schema); err != nil {
			t.Fatal(err)
		}
		if schema["@type"] == "ItemList" {
			items := schema["itemListElement"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["description"] != "原因 </script><script>bad()</script>" {
				t.Fatal(schema)
			}
		}
	}
	if strings.Contains(detail, "</script><script>bad()") {
		t.Fatal("unescaped script")
	}
	editor := request("GET", "/admin/collections?edit=list-0", nil, true).Body.String()
	if !strings.Contains(editor, `value="`+strconv.Itoa(ids[2])+`" selected`) || !strings.Contains(editor, "继续看封闭环境的人物博弈") {
		t.Fatal("editor lost relation")
	}
	if strings.Contains(request("GET", "/list/list-1", nil, false).Body.String(), "继续看片单") {
		t.Fatal("relation became bidirectional")
	}
	// Removing every relation must hide the entire section.
	form.Set("related_id_0", "")
	form.Set("related_id_1", "")
	if got := request("POST", "/admin/collections", form, true); got.Code != 302 {
		t.Fatal(got.Code)
	}
	if strings.Contains(request("GET", "/list/list-0", nil, false).Body.String(), "继续看片单") {
		t.Fatal("empty section visible")
	}
}

func TestRelatedCollectionsLifecycleAndRollback(t *testing.T) {
	pool := testdb.Pool(t)
	store := NewPostgresStore(pool)
	testdb.Media(t, pool, 1)
	if _, err := pool.Exec(t.Context(), `UPDATE media SET douban_id='film' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	items := []ItemInput{{DoubanID: "film", Note: "入选理由"}}
	makeCollection := func(slug string) Collection {
		c := Collection{Slug: slug, Title: slug, Description: "选片范围", Featured: true}
		var err error
		c.ID, _, err = store.Save(t.Context(), c, items)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	source := makeCollection("source")
	a := makeCollection("target-a")
	b := makeCollection("target-b")
	c := makeCollection("target-c")
	source.Related = []RelatedInput{{ID: b.ID, Reason: "推荐 B"}, {ID: a.ID}, {ID: c.ID}}
	if _, _, err := store.Save(t.Context(), source, items); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]RelatedInput{{{ID: source.ID}}, {{ID: a.ID}, {ID: a.ID}}, {{ID: 99999999}}, {{ID: a.ID}, {ID: b.ID}, {ID: c.ID}, {ID: source.ID}}} {
		candidate := source
		candidate.Title = "不应保存"
		candidate.Related = invalid
		if _, _, err := store.Save(t.Context(), candidate, items); !errors.Is(err, ErrInvalidRelated) {
			t.Fatalf("invalid relation: %v", err)
		}
		kept, err := store.GetBySlug(t.Context(), source.Slug)
		if err != nil || kept.Title != source.Title {
			t.Fatal("failed save changed source")
		}
	}
	b.Featured = false
	if _, _, err := store.Save(t.Context(), b, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListRelated(t.Context(), source.ID, true); err != nil || len(got) != 2 || got[0].ID != a.ID {
		t.Fatalf("draft filter %v %v", got, err)
	}
	// Existing hidden relations survive editing, but cannot be newly attached elsewhere.
	if _, _, err := store.Save(t.Context(), source, items); err != nil {
		t.Fatal(err)
	}
	a.Related = []RelatedInput{{ID: b.ID}}
	if _, _, err := store.Save(t.Context(), a, items); !errors.Is(err, ErrInvalidRelated) {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM collection_items WHERE collection_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListRelated(t.Context(), source.ID, true); err != nil || len(got) != 1 || got[0].ID != c.ID {
		t.Fatal("empty target not hidden", err)
	}
	b.Featured = true
	if _, _, err := store.Save(t.Context(), b, items); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListRelated(t.Context(), source.ID, true); err != nil || len(got) != 2 || got[0].ID != b.ID {
		t.Fatal("republish order", err)
	}
	if err := store.Delete(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListRelated(t.Context(), source.ID, false); err != nil || len(got) != 2 {
		t.Fatal("deleted target relation not removed", err)
	}
	if err := store.Delete(t.Context(), source.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListRelated(t.Context(), source.ID, false); err != nil || len(got) != 0 {
		t.Fatal("deleted source relation not removed", err)
	}
}
