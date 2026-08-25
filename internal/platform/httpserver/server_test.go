package httpserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

func testConfig() config.Config {
	return config.Config{Env: "test", Port: "5008", SiteName: "Moovie影牛", SiteURL: "http://localhost:5008"}
}

func TestHealth(t *testing.T) {
	server := New(testConfig(), nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := recorder.Header().Get("X-Request-ID"); len(got) != 32 {
		t.Fatalf("generated X-Request-ID = %q", got)
	}
}

func TestResponsesUseGzipWithoutEnablingCrossOriginAccess(t *testing.T) {
	const body = "Moovie transport compression contract"
	server := New(testConfig(), nil, func(router *gin.Engine) {
		router.GET("/compressed", func(c *gin.Context) { c.String(http.StatusOK, body) })
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/compressed", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Origin", "https://untrusted.example")
	server.Handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("same-origin policy emitted Access-Control-Allow-Origin = %q", got)
	}
	reader, err := gzip.NewReader(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != body {
		t.Fatalf("decoded body = %q, want %q", decoded, body)
	}
}

func TestRequestIDPreservesValidCallerValueAndReplacesUnsafeValue(t *testing.T) {
	server := New(testConfig(), nil, nil)
	valid := httptest.NewRecorder()
	validRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	validRequest.Header.Set("X-Request-ID", "edge-123")
	server.Handler.ServeHTTP(valid, validRequest)
	if valid.Header().Get("X-Request-ID") != "edge-123" {
		t.Fatalf("valid request ID = %q", valid.Header().Get("X-Request-ID"))
	}
	unsafe := httptest.NewRecorder()
	unsafeRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	unsafeRequest.Header.Set("X-Request-ID", "bad value with spaces")
	server.Handler.ServeHTTP(unsafe, unsafeRequest)
	if got := unsafe.Header().Get("X-Request-ID"); got == "bad value with spaces" || len(got) != 32 {
		t.Fatalf("unsafe request ID was not replaced: %q", got)
	}
}

func TestReadyReportsDependencyFailure(t *testing.T) {
	server := New(testConfig(), func(context.Context) error { return errors.New("database unavailable") }, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestCSRFProtectionRejectsMissingAndMismatchedTokens(t *testing.T) {
	server := New(testConfig(), nil, func(router *gin.Engine) {
		router.POST("/mutate", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})

	missing := httptest.NewRecorder()
	server.Handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/mutate", nil))
	if missing.Code != http.StatusForbidden {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusForbidden)
	}
	if got := missing.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("CSRF rejection X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := missing.Header().Get("X-Request-ID"); len(got) != 32 {
		t.Fatalf("CSRF rejection X-Request-ID = %q", got)
	}
	cookie := responseCookie(t, missing, csrfCookieName)

	mismatch := httptest.NewRecorder()
	mismatchRequest := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	mismatchRequest.AddCookie(cookie)
	mismatchRequest.Header.Set(csrfHeaderName, strings.Repeat("0", 64))
	server.Handler.ServeHTTP(mismatch, mismatchRequest)
	if mismatch.Code != http.StatusForbidden {
		t.Fatalf("mismatched token status = %d, want %d", mismatch.Code, http.StatusForbidden)
	}
}

func TestCSRFRejectionIsLogged(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(previous)

	server := New(testConfig(), nil, func(router *gin.Engine) {
		router.POST("/mutate", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/mutate", nil))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	logLine := output.String()
	for _, field := range []string{"method=POST", "path=/mutate", "status=403", "request_id="} {
		if !strings.Contains(logLine, field) {
			t.Fatalf("request log %q does not contain %q", logLine, field)
		}
	}
}

func TestCSRFProtectionAcceptsHeaderAndLegacyForm(t *testing.T) {
	server := New(testConfig(), nil, func(router *gin.Engine) {
		router.POST("/mutate", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})
	cookie := csrfCookie(t, server)

	headerResponse := httptest.NewRecorder()
	headerRequest := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	headerRequest.AddCookie(cookie)
	headerRequest.Header.Set(csrfHeaderName, cookie.Value)
	server.Handler.ServeHTTP(headerResponse, headerRequest)
	if headerResponse.Code != http.StatusNoContent {
		t.Fatalf("header token status = %d, want %d", headerResponse.Code, http.StatusNoContent)
	}

	form := url.Values{csrfCookieName: {cookie.Value}}
	formResponse := httptest.NewRecorder()
	formRequest := httptest.NewRequest(http.MethodPost, "/mutate", strings.NewReader(form.Encode()))
	formRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formRequest.AddCookie(cookie)
	server.Handler.ServeHTTP(formResponse, formRequest)
	if formResponse.Code != http.StatusNoContent {
		t.Fatalf("form token status = %d, want %d", formResponse.Code, http.StatusNoContent)
	}
}

func TestCSRFCookieAttributesAndNoTelemetryExemption(t *testing.T) {
	cfg := testConfig()
	cfg.Env = "production"
	server := New(cfg, nil, func(router *gin.Engine) {
		router.POST("/api/report/load-speed", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})

	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/report/load-speed", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("retired telemetry status = %d, want %d", response.Code, http.StatusForbidden)
	}
	cookie := responseCookie(t, response, csrfCookieName)
	if !cookie.Secure || cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("unexpected CSRF cookie attributes: %+v", cookie)
	}
}

func csrfCookie(t *testing.T, server *http.Server) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	return responseCookie(t, response, csrfCookieName)
}

func responseCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	response := &http.Response{Header: recorder.Header(), Body: io.NopCloser(strings.NewReader(recorder.Body.String()))}
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q missing", name)
	return nil
}
