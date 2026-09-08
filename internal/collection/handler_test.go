package collection

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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

func TestCollectionSaveErrorsPreserveFormAndPublicEmptyIsHidden(t *testing.T) {
	pool := testdb.Pool(t)
	store := NewPostgresStore(pool)
	testdb.Media(t, pool, 1)
	if _, err := pool.Exec(t.Context(), `UPDATE media SET douban_id = '1292052' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	id, _, err := store.Save(t.Context(), Collection{Slug: "existing", Title: "原片单", Featured: true, Description: "选片说明"}, []ItemInput{{DoubanID: "1292052", Note: "原推荐语"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(t.Context(), Collection{Slug: "duplicate", Title: "占用地址"}, nil); err != nil {
		t.Fatal(err)
	}
	renderer, err := web.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"collection", "collections", "admin_collections", "404"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(config.Config{AppSecret: "secret", SiteName: "Moovie", Env: "test"}, store).Register(router)
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
	for _, slug := range []string{"duplicate", ""} {
		form := url.Values{"id": {strconv.Itoa(id)}, "title": {"编辑后的中文标题"}, "slug": {slug}, "description": {"未保存的导语"}, "items": {"1292052 | 未保存推荐语\nmissing | 保留此行"}, "featured": {"on"}}
		response := request(http.MethodPost, "/admin/collections", form, true)
		if slug == "duplicate" && (!strings.Contains(response.Body.String(), "这个片单地址已被使用") || strings.Contains(response.Body.String(), "SQLSTATE")) {
			t.Fatal("duplicate address should have an actionable message")
		}
		if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" {
			t.Fatalf("validation = %d", response.Code)
		}
		for _, value := range []string{`name="id" value="` + strconv.Itoa(id) + `"`, "编辑后的中文标题", "未保存的导语", "未保存推荐语", "missing | 保留此行", `name="featured" checked`} {
			if !strings.Contains(response.Body.String(), value) {
				t.Fatalf("form lost %q", value)
			}
		}
	}
	invalid := request(http.MethodPost, "/admin/collections", url.Values{"title": {"空片单"}, "slug": {"new-empty"}, "items": {"missing | 不应丢失"}, "featured": {"on"}}, true)
	if invalid.Code != http.StatusUnprocessableEntity || !strings.Contains(invalid.Body.String(), "missing | 不应丢失") {
		t.Fatalf("empty publication = %d", invalid.Code)
	}
	if absent, _ := store.GetBySlug(t.Context(), "new-empty"); absent != nil {
		t.Fatal("empty collection persisted")
	}
	// 历史空片单即使 featured=true，也不对游客开放，管理员预览禁止索引。
	if _, err := pool.Exec(t.Context(), `INSERT INTO collections(slug,title,featured) VALUES('legacy-empty','历史空片单',true)`); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, "/list/legacy-empty", nil, false); response.Code != http.StatusNotFound {
		t.Fatalf("public empty = %d", response.Code)
	}
	if response := request(http.MethodGet, "/list/legacy-empty", nil, true); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "noindex, nofollow") {
		t.Fatalf("admin empty preview = %d", response.Code)
	}
	// 改正输入后仍更新原片单，正常发布继续可用。
	valid := request(http.MethodPost, "/admin/collections", url.Values{"id": {strconv.Itoa(id)}, "title": {"修正后的片单"}, "slug": {"existing"}, "description": {"选片说明"}, "items": {"1292052 | 新推荐语"}, "featured": {"on"}}, true)
	if valid.Code != http.StatusFound {
		t.Fatalf("valid save = %d/%s", valid.Code, valid.Body.String())
	}
	if response := request(http.MethodGet, "/list/existing", nil, false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "新推荐语") {
		t.Fatalf("published detail = %d", response.Code)
	}
}
