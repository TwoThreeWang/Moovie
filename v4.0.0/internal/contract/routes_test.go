package contract

import (
	"fmt"
	"strings"
	"testing"
)

func TestFinalRouteInventory(t *testing.T) {
	const expected = 121
	if len(Routes) != expected {
		t.Fatalf("route count = %d, want %d", len(Routes), expected)
	}

	seen := make(map[string]struct{}, len(Routes))
	for _, route := range Routes {
		if !strings.HasPrefix(route.Path, "/") {
			t.Errorf("route path %q must start with /", route.Path)
		}
		key := fmt.Sprintf("%s %s", route.Method, route.Path)
		if _, exists := seen[key]; exists {
			t.Errorf("duplicate route %s", key)
		}
		seen[key] = struct{}{}
	}
}

func TestCriticalIndexedRoutesRemainInContract(t *testing.T) {
	required := []string{
		"GET /",
		"GET /search",
		"GET /movie/:id",
		"GET /play/:source_key/:vod_id",
		"GET /discover/:type",
		"GET /cinema",
		"GET /similar/:douban_id",
		"GET /user/:user_id",
		"GET /user/:user_id/monthly/:year_month",
		"GET /sitemap.xml",
		"GET /robots.txt",
	}

	available := make(map[string]struct{}, len(Routes))
	for _, route := range Routes {
		available[route.Method+" "+route.Path] = struct{}{}
	}
	for _, key := range required {
		if _, ok := available[key]; !ok {
			t.Errorf("critical route %s is missing", key)
		}
	}
}
