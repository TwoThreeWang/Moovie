package feedback

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
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestAnonymousAndAuthenticatedFeedbackListsPreservePrivacyAndValidation(t *testing.T) {
	router, store, userToken, _ := feedbackTestRouter(t)
	anonymous := formRequest(router, http.MethodPost, "/api/feedback", url.Values{
		"type": {"bug"}, "content": {"<script>问题</script>"}, "movie_url": {"https://moovie.example/movie/1"},
	}, "")
	if anonymous.Code != http.StatusOK || !strings.Contains(anonymous.Body.String(), "感谢您的反馈") {
		t.Fatalf("anonymous submit = %d/%s", anonymous.Code, anonymous.Body.String())
	}
	authenticated := formRequest(router, http.MethodPost, "/api/feedback", url.Values{
		"type": {"request"}, "content": {"想看这部电影"},
	}, userToken)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated submit = %d/%s", authenticated.Code, authenticated.Body.String())
	}
	public := request(router, http.MethodGet, "/api/htmx/feedback-list?type=bug", "")
	if public.Code != http.StatusOK || !strings.Contains(public.Body.String(), "&lt;script&gt;问题&lt;/script&gt;") || strings.Contains(public.Body.String(), "想看这部电影") {
		t.Fatalf("public list = %d/%s", public.Code, public.Body.String())
	}
	guestDashboard := request(router, http.MethodGet, "/api/htmx/dashboard/feedback", "")
	if guestDashboard.Code != http.StatusOK || guestDashboard.Body.String() != "未登录" {
		t.Fatalf("guest dashboard = %d/%q", guestDashboard.Code, guestDashboard.Body.String())
	}
	userDashboard := request(router, http.MethodGet, "/api/htmx/dashboard/feedback", userToken)
	if userDashboard.Code != http.StatusOK || !strings.Contains(userDashboard.Body.String(), "想看这部电影") || strings.Contains(userDashboard.Body.String(), "问题") {
		t.Fatalf("user dashboard = %d/%s", userDashboard.Code, userDashboard.Body.String())
	}
	if count, _ := store.CountByUser(t.Context(), 1); count != 1 {
		t.Fatalf("authenticated feedback count = %d", count)
	}

	for name, values := range map[string]url.Values{
		"blank":        {"type": {"bug"}, "content": {"   "}},
		"invalid type": {"type": {"other"}, "content": {"x"}},
		"system alert": {"type": {TypeSystemAlert}, "content": {"伪造告警"}},
		"unsafe url":   {"type": {"bug"}, "content": {"x"}, "movie_url": {"javascript:alert(1)"}},
		"too long":     {"type": {"bug"}, "content": {strings.Repeat("长", 5001)}},
	} {
		t.Run(name, func(t *testing.T) {
			response := formRequest(router, http.MethodPost, "/api/feedback", values, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminFeedbackRequiresRoleAndPreservesStatusReplyEnvelope(t *testing.T) {
	router, store, userToken, adminToken := feedbackTestRouter(t)
	created, _ := store.Create(t.Context(), Feedback{Type: "suggestion", Content: "增加筛选"})
	forbidden := request(router, http.MethodGet, "/admin/feedback", userToken)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "需要管理员权限") {
		t.Fatalf("forbidden = %d/%s", forbidden.Code, forbidden.Body.String())
	}
	admin := request(router, http.MethodGet, "/admin/feedback?status=pending", adminToken)
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), "增加筛选") || !strings.Contains(admin.Body.String(), "反馈管理 - Moovie影牛") {
		t.Fatalf("admin page = %d/%s", admin.Code, admin.Body.String())
	}
	status := formRequest(router, http.MethodPut, "/admin/feedback/"+itoa(created.ID)+"/status", url.Values{"status": {"rejected"}}, adminToken)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"success":true`) || !strings.Contains(status.Body.String(), "状态已更新") {
		t.Fatalf("status = %d/%s", status.Code, status.Body.String())
	}
	reply := formRequest(router, http.MethodPut, "/admin/feedback/"+itoa(created.ID)+"/reply", url.Values{"reply": {"已安排"}}, adminToken)
	if reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), "回复成功") {
		t.Fatalf("reply = %d/%s", reply.Code, reply.Body.String())
	}
	records, _ := store.ListAdmin(t.Context(), "resolved", 10, 0)
	if len(records) != 1 || records[0].Reply != "已安排" || records[0].RepliedAt == nil {
		t.Fatalf("resolved records = %+v", records)
	}
	invalid := formRequest(router, http.MethodPut, "/admin/feedback/"+itoa(created.ID)+"/status", url.Values{"status": {"deleted"}}, adminToken)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"success":false`) {
		t.Fatalf("invalid status = %d/%s", invalid.Code, invalid.Body.String())
	}
}

func feedbackTestRouter(t *testing.T) (*gin.Engine, *PostgresStore, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	testdb.User(t, testdb.Pool(t), 1, 2)
	store := NewPostgresStore(testdb.Pool(t))
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"feedback", "admin_feedback"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, store).Register(router)
	now := time.Now()
	userToken, _ := auth.Sign(auth.Claims{UserID: 1, Email: "user@example.com", Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	adminToken, _ := auth.Sign(auth.Claims{UserID: 2, Email: "admin@example.com", Role: "admin", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, store, userToken, adminToken
}

func request(router http.Handler, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func formRequest(router http.Handler, method, target string, values url.Values, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func itoa(value int) string { return strconv.Itoa(value) }
