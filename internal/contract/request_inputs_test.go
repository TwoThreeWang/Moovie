package contract

import (
	"fmt"
	"testing"
)

func TestFinalRequestInputInventory(t *testing.T) {
	routes := make(map[string]struct{}, len(Routes))
	for _, route := range Routes {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	seen := make(map[string]struct{}, len(RequestInputs))
	validLocations := map[InputLocation]bool{
		InputQuery: true, InputForm: true, InputFormOrQuery: true, InputJSON: true, InputHeader: true,
	}
	for _, input := range RequestInputs {
		routeKey := input.Method + " " + input.Path
		if _, ok := routes[routeKey]; !ok {
			t.Errorf("request input %s %q belongs to an unknown route", routeKey, input.Name)
		}
		if input.Name == "" || !validLocations[input.Location] {
			t.Errorf("invalid request input: %+v", input)
		}
		key := fmt.Sprintf("%s %s %s %s", input.Method, input.Path, input.Location, input.Name)
		if _, ok := seen[key]; ok {
			t.Errorf("duplicate request input %s", key)
		}
		seen[key] = struct{}{}
	}
}

func TestCriticalRequestFieldsRemainInContract(t *testing.T) {
	required := []string{
		"GET /search query doubanId",
		"GET /api/htmx/search query q",
		"GET /api/htmx/search query douban_id",
		"POST /api/user-movies/:id/watched form_or_query rating",
		"POST /api/v2/history/sync json operations[].occurred_at",
		"POST /api/v2/playback/events json source_key",
		"POST /api/danmaku json color",
		"GET /admin/sites/:id/test query keyword",
	}
	available := make(map[string]struct{}, len(RequestInputs))
	for _, input := range RequestInputs {
		available[fmt.Sprintf("%s %s %s %s", input.Method, input.Path, input.Location, input.Name)] = struct{}{}
	}
	for _, key := range required {
		if _, ok := available[key]; !ok {
			t.Errorf("critical request field %s is missing", key)
		}
	}
}
