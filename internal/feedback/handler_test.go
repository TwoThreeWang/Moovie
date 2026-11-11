package feedback

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/pow"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestAnonymousAndAuthenticatedFeedbackListsPreservePrivacyAndValidation(t *testing.T) {
	router, store, userToken, _ := feedbackTestRouter(t)
	anonymous := submitFeedback(t, router, url.Values{
		"type": {"bug"}, "content": {"<script>问题</script>"}, "movie_url": {"https://moovie.example/movie/1"},
	}, "")
	if anonymous.Code != http.StatusOK || !strings.Contains(anonymous.Body.String(), "感谢您的反馈") {
		t.Fatalf("anonymous submit = %d/%s", anonymous.Code, anonymous.Body.String())
	}
	authenticated := submitFeedback(t, router, url.Values{
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

	// 校验失败返回整个表单片段（200，HTMX 才会替换），带错误提示、已填内容和新挑战。
	for name, testCase := range map[string]struct {
		values url.Values
		want   string
	}{
		"blank":        {url.Values{"type": {"bug"}, "content": {"   "}}, "请填写反馈内容"},
		"invalid type": {url.Values{"type": {"other"}, "content": {"x"}}, "反馈类型无效"},
		"system alert": {url.Values{"type": {TypeSystemAlert}, "content": {"伪造告警"}}, "反馈类型无效"},
		"unsafe url":   {url.Values{"type": {"suggestion"}, "content": {"保留我"}, "movie_url": {"javascript:alert(1)"}}, "相关链接无效"},
		"too long":     {url.Values{"type": {"bug"}, "content": {strings.Repeat("长", 5001)}}, "反馈内容不能超过 5000 个字符"},
	} {
		t.Run(name, func(t *testing.T) {
			response := submitFeedback(t, router, testCase.values, "")
			body := response.Body.String()
			if response.Code != http.StatusOK || !strings.Contains(body, testCase.want) || !strings.Contains(body, `<form class="feedback-form"`) {
				t.Fatalf("response = %d/%s", response.Code, body)
			}
			challengeFrom(t, body)
		})
	}
	kept := submitFeedback(t, router, url.Values{"type": {"suggestion"}, "content": {"保留我"}, "movie_url": {"javascript:alert(1)"}}, "")
	if body := kept.Body.String(); !strings.Contains(body, `<option value="suggestion" selected>`) || !strings.Contains(body, ">保留我</textarea>") {
		t.Fatalf("form values not kept: %s", body)
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
	NewHandler(cfg, store, WithHumanCheck(steppingIssuer(t))).Register(router)
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

// steppingIssuer 返回难度 4 位、每次读时钟都前进 3 秒的签发器：
// 既能快速求解，又天然满足「签发后至少 2 秒才能提交」。
func steppingIssuer(t *testing.T) *pow.Issuer {
	t.Helper()
	issuer, err := pow.NewIssuer(4)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	issuer.SetClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(3 * time.Second)
		return clock
	})
	return issuer
}

var powTokenPattern = regexp.MustCompile(`name="pow_token" value="([^"]*)"`)

// challengeFrom 取出页面或片段里的挑战，没有就让测试失败。
func challengeFrom(t *testing.T, body string) string {
	t.Helper()
	match := powTokenPattern.FindStringSubmatch(body)
	if match == nil || match[1] == "" {
		t.Fatalf("no pow challenge in: %s", body)
	}
	return match[1]
}

// submitFeedback 像浏览器一样先打开反馈页拿挑战、求解，再提交。
func submitFeedback(t *testing.T, router http.Handler, values url.Values, token string) *httptest.ResponseRecorder {
	t.Helper()
	page := request(router, http.MethodGet, "/feedback", token)
	challenge := challengeFrom(t, page.Body.String())
	solved := url.Values{}
	for key, list := range values {
		solved[key] = list
	}
	solved.Set(pow.TokenField, challenge)
	solved.Set(pow.NonceField, pow.Solve(challenge))
	return formRequest(router, http.MethodPost, "/api/feedback", solved, token)
}
