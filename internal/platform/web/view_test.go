package web

import "testing"

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
		{path: "/admin/sites", want: "admin"},
		{path: "/movie/1292052", want: ""},
	}
	for _, test := range tests {
		if got := ActiveMenu(test.path, test.searchType); got != test.want {
			t.Errorf("ActiveMenu(%q, %q) = %q, want %q", test.path, test.searchType, got, test.want)
		}
	}
}
