package catalog

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestImageProxyPreservesLegacyEncodingHeadersAndBody(t *testing.T) {
	var upstreamRequest *http.Request
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamRequest = request
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg"}},
			Body: io.NopCloser(strings.NewReader("jpeg-body")), ContentLength: int64(len("jpeg-body")),
			Request: request,
		}, nil
	})}
	router := imageProxyRouter(t, client)
	encoded := proxyImageURL("https://img9.doubanio.com/view/photo.jpg")
	request := sameOriginImageRequest(encoded)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "jpeg-body" || recorder.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("proxy response = %d/%q/%q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "private, max-age=2592000" || recorder.Header().Get("Expires") == "" ||
		recorder.Header().Get("Vary") != "Sec-Fetch-Site, Sec-Fetch-Dest, Sec-Fetch-Mode" ||
		recorder.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("cache headers = %#v", recorder.Header())
	}
	if upstreamRequest == nil || upstreamRequest.URL.Host != "img3.doubanio.com" || upstreamRequest.Header.Get("Referer") != "https://movie.douban.com/" || upstreamRequest.Header.Get("User-Agent") == "" {
		t.Fatalf("upstream request = %+v", upstreamRequest)
	}
}

func TestImageProxyAcceptsDynamicPublicImageHost(t *testing.T) {
	var upstreamRequest *http.Request
	router := imageProxyRouter(t, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamRequest = request
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg"}},
			Body: io.NopCloser(strings.NewReader("resource-image")), ContentLength: 14, Request: request}, nil
	})})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, sameOriginImageRequest(proxyImageURL("https://mtzy2.com/upload/poster.jpg")))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "resource-image" || upstreamRequest == nil ||
		upstreamRequest.URL.Hostname() != "mtzy2.com" || upstreamRequest.Header.Get("Referer") != "https://mtzy2.com/" {
		t.Fatalf("dynamic image proxy = %d/%q request=%+v", recorder.Code, recorder.Body.String(), upstreamRequest)
	}
}

func TestImageProxyReturnsSVGForHotlinkAndRejectsMalformedEncodingAndSSRF(t *testing.T) {
	router := imageProxyRouter(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("blocked proxy reached upstream")
		return nil, nil
	})})

	hotlinkRequest := sameOriginImageRequest(proxyImageURL("https://img3.doubanio.com/a.jpg"))
	hotlinkRequest.Header.Set("Sec-Fetch-Site", "cross-site")
	hotlink := httptest.NewRecorder()
	router.ServeHTTP(hotlink, hotlinkRequest)
	if hotlink.Code != http.StatusOK || hotlink.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" ||
		hotlink.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(hotlink.Body.String(), "仅限 Moovie 内部使用") {
		t.Fatalf("hotlink response = %d/%#v/%s", hotlink.Code, hotlink.Header(), hotlink.Body.String())
	}

	malformed := httptest.NewRecorder()
	router.ServeHTTP(malformed, sameOriginImageRequest("/api/proxy/image/not-valid"))
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), "非法的图片代理链接") {
		t.Fatalf("malformed response = %d/%s", malformed.Code, malformed.Body.String())
	}

	privateTarget := proxyImageURL("http://127.0.0.1/admin")
	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, sameOriginImageRequest(privateTarget))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("private target status = %d", blocked.Code)
	}
}

func TestImageProxyPassesThroughUpstreamStatus(t *testing.T) {
	router := imageProxyRouter(t, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing")), Request: request}, nil
	})})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, sameOriginImageRequest(proxyImageURL("https://image.tmdb.org/t/p/test.jpg")))
	if recorder.Code != http.StatusNotFound || recorder.Body.Len() != 0 {
		t.Fatalf("upstream status/body = %d/%q", recorder.Code, recorder.Body.String())
	}
}

func TestImageProxyRejectsNonImageResponse(t *testing.T) {
	router := imageProxyRouter(t, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}},
			Body: io.NopCloser(strings.NewReader("<html>not an image</html>")), Request: request}, nil
	})})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, sameOriginImageRequest(proxyImageURL("https://mtzy2.com/not-image")))
	if recorder.Code != http.StatusUnsupportedMediaType || recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("non-image response = %d/%#v", recorder.Code, recorder.Header())
	}
}

func TestImageProxyRejectsRedirectToUnsafeTarget(t *testing.T) {
	requests := 0
	router := imageProxyRouter(t, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Hostname() != "img3.doubanio.com" {
			t.Fatalf("redirect reached blocked host %q", request.URL.Hostname())
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://127.0.0.1/admin"}},
			Body: io.NopCloser(strings.NewReader("redirect")), Request: request}, nil
	})})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, sameOriginImageRequest(proxyImageURL("https://img3.doubanio.com/a.jpg")))
	if recorder.Code != http.StatusForbidden || requests != 1 {
		t.Fatalf("redirect response/requests = %d/%d", recorder.Code, requests)
	}
}

func TestImageProxyRejectsCredentialsAndNonDefaultPort(t *testing.T) {
	for _, target := range []string{
		"https://user:secret@img3.doubanio.com/a.jpg",
		"https://image.tmdb.org:8443/a.jpg",
	} {
		if _, err := validateImageProxyTarget(target); !errors.Is(err, errUnsafeImageProxyTarget) {
			t.Fatalf("unsafe target allowed: %s", target)
		}
	}
}

func TestImageProxyPublicIPClassification(t *testing.T) {
	for _, test := range []struct {
		address string
		public  bool
	}{
		{"8.8.8.8", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"::1", false},
		{"fc00::1", false},
		{"2001:db8::1", false},
	} {
		if got := isPublicImageProxyIP(netip.MustParseAddr(test.address)); got != test.public {
			t.Fatalf("isPublicImageProxyIP(%s) = %t, want %t", test.address, got, test.public)
		}
	}
	if _, err := safeImageProxyDialContext(t.Context(), "tcp", "127.0.0.1:80"); !errors.Is(err, errUnsafeImageProxyTarget) {
		t.Fatalf("private address dial error = %v", err)
	}
}

func imageProxyRouter(t *testing.T, client *http.Client) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(config.Config{SiteURL: "https://moovie.example"}, NewPostgresStore(testdb.Pool(t)), WithHTTPClient(client)).Register(router)
	return router
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func sameOriginImageRequest(path string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Dest", "image")
	request.Header.Set("Sec-Fetch-Mode", "no-cors")
	return request
}
