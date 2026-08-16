package danmaku

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

func TestHTTPContractAlwaysReturnsArrayAndRequiresLoginToSend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	service := NewService(store, nil, "")
	router := gin.New()
	NewHandler(config.Config{AppSecret: "secret"}, service).Register(router)

	empty := httptest.NewRecorder()
	router.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/api/danmaku?title=", nil))
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("empty list = %d/%s", empty.Code, empty.Body.String())
	}
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/danmaku", strings.NewReader(`{"title":"三体","text":"好看"}`)))
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Body.String(), "请先登录") {
		t.Fatalf("unauthorized = %d/%s", unauthorized.Code, unauthorized.Body.String())
	}
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: 7, Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	// 旧绑定器会忽略额外客户端元数据。继续接受这些字段，避免旧播放器增加字段后
	// 把原本有效的发送请求变成 400。
	request := httptest.NewRequest(http.MethodPost, "/api/danmaku", strings.NewReader(`{"title":"三体","episode":"第一集","text":"好看","time":2,"mode":1,"color":"#ff0000","client_version":"legacy-web"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	sent := httptest.NewRecorder()
	router.ServeHTTP(sent, request)
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"ok":true`) {
		t.Fatalf("sent = %d/%s", sent.Code, sent.Body.String())
	}
	listed := httptest.NewRecorder()
	router.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/danmaku?title=三体&episode=1", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"text":"好看"`) || !strings.Contains(listed.Body.String(), `"color":"#FF0000"`) {
		t.Fatalf("listed = %d/%s", listed.Code, listed.Body.String())
	}
}
