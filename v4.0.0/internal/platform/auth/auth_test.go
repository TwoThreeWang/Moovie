package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractAcceptsLegacyCookieAndBearerToken(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	token, err := Sign(Claims{UserID: 42, Email: "user@example.com", Role: "user", Issued: now.Add(-time.Hour).Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		cookie bool
	}{
		{name: "cookie", cookie: true}, {name: "bearer"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/", nil)
			if testCase.cookie {
				request.AddCookie(&http.Cookie{Name: "token", Value: token})
			} else {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			claims, err := Extract(request, "secret", now)
			if err != nil || claims.UserID != 42 || claims.Email != "user@example.com" {
				t.Fatalf("claims/error = %+v/%v", claims, err)
			}
		})
	}
}

func TestParseRejectsExpiredTamperedAndWrongAlgorithmTokens(t *testing.T) {
	now := time.Now()
	expired, _ := Sign(Claims{UserID: 1, Expiry: now.Add(-time.Second).Unix()}, "secret")
	if _, err := Parse(expired, "secret", now); err == nil {
		t.Fatal("expired token accepted")
	}
	valid, _ := Sign(Claims{UserID: 1, Expiry: now.Add(time.Hour).Unix()}, "secret")
	if _, err := Parse(valid+"x", "secret", now); err == nil {
		t.Fatal("tampered token accepted")
	}
}
