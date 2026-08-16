package catalog

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEmbeddingServiceUsesLocalMetadataAndPersistsExactly768Dimensions(t *testing.T) {
	var vectorCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/embeddings":
			vectorCalls.Add(1)
			var payload struct {
				Model  string `json:"model"`
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if payload.Model != "bge-test" || !strings.Contains(payload.Prompt, "标题：肖申克的救赎") || !strings.Contains(payload.Prompt, "剧情：越狱") {
				t.Fatalf("embedding payload = %+v", payload)
			}
			vector := make([]float32, embeddingDimensions)
			for index := range vector {
				vector[index] = float32(index) / 1000
			}
			encoded, _ := json.Marshal(map[string]any{"embedding": vector})
			return testJSONResponse(request, http.StatusOK, string(encoded)), nil
		default:
			return testJSONResponse(request, http.StatusNotFound, `{}`), nil
		}
	})}

	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎", Summary: "越狱", Directors: `[{"name":"导演甲"}]`, Actors: `[{"name":"演员甲"}]`})
	service := NewEmbeddingService(client, store, EmbeddingConfig{
		OllamaHost: "https://ollama.test", OllamaModel: "bge-test",
	})
	if err := service.Enrich(t.Context(), "1292052"); err != nil {
		t.Fatal(err)
	}
	movie, _ := store.FindByDoubanID(t.Context(), "1292052")
	if vectorCalls.Load() != 1 || movie.EmbeddingSemanticHash == "" || len(movie.Embedding) != embeddingDimensions {
		t.Fatalf("calls/movie = %d/%+v", vectorCalls.Load(), movie)
	}
}

func TestEmbeddingServiceFallsBackToMetadataAndRejectsWrongDimension(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/embeddings" {
			var payload struct {
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if !strings.Contains(payload.Prompt, "导演：导演甲") || !strings.Contains(payload.Prompt, "主演：演员1,演员2,演员3,演员4,演员5") || strings.Contains(payload.Prompt, "演员6") {
				t.Fatalf("fallback prompt = %q", payload.Prompt)
			}
			return testJSONResponse(request, http.StatusOK, `{"embedding":[0.1,0.2]}`), nil
		}
		return testJSONResponse(request, http.StatusNotFound, `{}`), nil
	})}

	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "1292052", Title: "标题", Genres: "剧情", Summary: "简介", Directors: `[{"name":"导演甲"}]`,
		Actors: `[{"name":"演员1"},{"name":"演员2"},{"name":"演员3"},{"name":"演员4"},{"name":"演员5"},{"name":"演员6"}]`,
	})
	service := NewEmbeddingService(client, store, EmbeddingConfig{
		OllamaHost: "https://ollama.test",
	})
	err := service.Enrich(t.Context(), "1292052")
	if err == nil || !strings.Contains(err.Error(), "want 768, got 2") {
		t.Fatalf("error = %v", err)
	}
	movie, _ := store.FindByDoubanID(t.Context(), "1292052")
	if movie.EmbeddingContent != "" || len(movie.Embedding) != 0 {
		t.Fatalf("invalid embedding was persisted: %+v", movie)
	}
}

func TestEmbeddingServiceOnlyRebuildsWhenSemanticInputChanges(t *testing.T) {
	var vectorCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		vectorCalls.Add(1)
		vector := make([]float32, embeddingDimensions)
		encoded, _ := json.Marshal(map[string]any{"embedding": vector})
		return testJSONResponse(request, http.StatusOK, string(encoded)), nil
	})}
	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1", Title: "标题", Summary: "简介"})
	service := NewEmbeddingService(client, store, EmbeddingConfig{OllamaHost: "https://ollama.test"})
	if err := service.Enrich(t.Context(), "1"); err != nil {
		t.Fatal(err)
	}
	if err := service.Enrich(t.Context(), "1"); err != nil {
		t.Fatal(err)
	}
	movie, _ := store.FindByDoubanID(t.Context(), "1")
	movie.Poster = "new-poster"
	_ = store.Upsert(t.Context(), *movie)
	if err := service.Enrich(t.Context(), "1"); err != nil {
		t.Fatal(err)
	}
	movie, _ = store.FindByDoubanID(t.Context(), "1")
	movie.Summary = "变化后的简介"
	_ = store.Upsert(t.Context(), *movie)
	if err := service.Enrich(t.Context(), "1"); err != nil {
		t.Fatal(err)
	}
	if vectorCalls.Load() != 2 {
		t.Fatalf("vector calls = %d, want 2", vectorCalls.Load())
	}
}
