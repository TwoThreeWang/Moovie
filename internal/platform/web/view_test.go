package web

import (
	"net/url"
	"testing"
)

func TestActiveMenuPreservesLegacyNavigationRules(t *testing.T) {
	tests := []struct {
		path       string
		searchType string
		want       string
	}{
		{path: "/", want: "home"},
		{path: "/discover", want: "discover"},
		{path: "/trends", want: "trends"},
		{path: "/foryou", want: "foryou"},
		{path: "/cinema", want: "cinema"},
		{path: "/player", want: "player"},
		{path: "/iptv", want: "iptv"},
		{path: "/feedback", want: "feedback"},
		{path: "/tvbox", want: ""},
		{path: "/about", want: "about"},
		{path: "/advertise", want: "advertise"},
		{path: "/search", want: "search"},
		{path: "/search", searchType: "movie", want: "movie"},
		{path: "/dashboard/settings", want: "user"},
		{path: "/notifications", want: "notifications"},
		{path: "/admin/sites", want: "admin"},
		{path: "/movie/1292052", want: ""},
	}
	for _, test := range tests {
		if got := ActiveMenu(test.path, test.searchType); got != test.want {
			t.Errorf("ActiveMenu(%q, %q) = %q, want %q", test.path, test.searchType, got, test.want)
		}
	}
}

func TestNextPageURLMapsOldPagesToTheirNewCounterparts(t *testing.T) {
	const next = "https://next.example"
	tests := map[string]string{
		"/movie/1292052?source_key=a&vod_id=1": next + "/movie/1292052",
		"/watch/1292052?source_key=a":          next + "/watch/1292052",
		"/similar/1292052":                     next + "/similar/1292052",
		"/search?q=%E9%9C%B8%E7%8E%8B+%26":     next + "/search?q=%E9%9C%B8%E7%8E%8B+%26",
		"/search":                              next + "/search",
		"/dashboard":                           next + "/me",
		"/dashboard/settings":                  next + "/settings",
		"/discover/cartoon":                    next + "/discover/anime",
		"/discover/trending":                   next + "/discover",
		"/discover/tv?page=2":                  next + "/discover/tv",
		"/list":                                next + "/list",
		"/list/some-slug":                      next + "/",
		"/play/source/42":                      next + "/",
		"/feed":                                next + "/",
		"/":                                    next + "/",
	}
	for raw, want := range tests {
		page, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := NextPageURL(next, page); got != want {
			t.Errorf("NextPageURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
