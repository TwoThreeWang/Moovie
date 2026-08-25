package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
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
			// 未配置 AI Gateway 时直接用元数据。剧情必须排在标题前面：
			// bge 的窗口只有 512 token，剧情排在末尾会被模型静默截掉。
			summaryAt := strings.Index(payload.Prompt, "越狱")
			titleAt := strings.Index(payload.Prompt, "肖申克的救赎")
			if payload.Model != "bge-test" || summaryAt < 0 || titleAt < 0 || summaryAt > titleAt {
				t.Fatalf("embedding payload = %+v", payload)
			}
			if strings.Contains(payload.Prompt, "标题：") || strings.Contains(payload.Prompt, "剧情：") {
				t.Fatalf("兜底文本不应保留字段标签: %q", payload.Prompt)
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

	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎", Summary: "越狱", Directors: `[{"name":"导演甲"}]`, Actors: `[{"name":"演员甲"}]`})
	markEmbeddingMetadataReady(t, store, "1292052")
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
			// 人名是高熵噪音，留太多会把相似度拉向「同演员」而不是「同题材」，主演只保留 3 个。
			if !strings.Contains(payload.Prompt, "导演甲") || !strings.Contains(payload.Prompt, "演员3") || strings.Contains(payload.Prompt, "演员4") {
				t.Fatalf("fallback prompt = %q", payload.Prompt)
			}
			return testJSONResponse(request, http.StatusOK, `{"embedding":[0.1,0.2]}`), nil
		}
		return testJSONResponse(request, http.StatusNotFound, `{}`), nil
	})}

	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "1292052", Title: "标题", Genres: "剧情", Summary: "简介", Directors: `[{"name":"导演甲"}]`,
		Actors: `[{"name":"演员1"},{"name":"演员2"},{"name":"演员3"},{"name":"演员4"},{"name":"演员5"},{"name":"演员6"}]`,
	})
	markEmbeddingMetadataReady(t, store, "1292052")
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

func TestEmbeddingServiceEmbedsAIRewriteWhenGatewayConfigured(t *testing.T) {
	const rewritten = "《肖申克的救赎》(The Shawshank Redemption) 体制压迫、希望母题、缓慢救赎。该片适合喜欢慢热剧情片的观众。"
	var gatewayCalls, vectorCalls atomic.Int32
	var embedded string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/chat/completions":
			gatewayCalls.Add(1)
			if request.Header.Get("Authorization") != "Bearer cf-token" {
				t.Fatalf("missing bearer token: %q", request.Header.Get("Authorization"))
			}
			encoded, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": rewritten}}},
			})
			return testJSONResponse(request, http.StatusOK, string(encoded)), nil
		case "/api/embeddings":
			vectorCalls.Add(1)
			var payload struct {
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			embedded = payload.Prompt
			vector := make([]float32, embeddingDimensions)
			encoded, _ := json.Marshal(map[string]any{"embedding": vector})
			return testJSONResponse(request, http.StatusOK, string(encoded)), nil
		default:
			return testJSONResponse(request, http.StatusNotFound, `{}`), nil
		}
	})}

	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎", Summary: "越狱"})
	markEmbeddingMetadataReady(t, store, "1292052")
	service := NewEmbeddingService(client, store, EmbeddingConfig{
		OllamaHost: "https://ollama.test", CFGatewayURL: "https://gateway.test", CFAPIToken: "cf-token", CFAIModel: "test-model",
	})
	if err := service.Enrich(t.Context(), "1292052"); err != nil {
		t.Fatal(err)
	}
	if gatewayCalls.Load() != 1 || vectorCalls.Load() != 1 {
		t.Fatalf("gateway/vector calls = %d/%d", gatewayCalls.Load(), vectorCalls.Load())
	}
	if embedded != rewritten {
		t.Fatalf("送进向量模型的应是 AI 改写结果，实际 = %q", embedded)
	}
}

func TestEmbeddingServiceFallsBackWhenGatewayKeepsFailing(t *testing.T) {
	var gatewayCalls atomic.Int32
	var embedded string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/chat/completions":
			gatewayCalls.Add(1)
			return testJSONResponse(request, http.StatusTooManyRequests, `{}`), nil
		case "/api/embeddings":
			var payload struct {
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			embedded = payload.Prompt
			vector := make([]float32, embeddingDimensions)
			encoded, _ := json.Marshal(map[string]any{"embedding": vector})
			return testJSONResponse(request, http.StatusOK, string(encoded)), nil
		default:
			return testJSONResponse(request, http.StatusNotFound, `{}`), nil
		}
	})}

	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎", Summary: "越狱"})
	markEmbeddingMetadataReady(t, store, "1292052")
	service := NewEmbeddingService(client, store, EmbeddingConfig{
		OllamaHost: "https://ollama.test", CFGatewayURL: "https://gateway.test", CFAPIToken: "cf-token",
	})
	service.retryDelays = nil // 不必真的等 3/5/8 秒。

	if err := service.Enrich(t.Context(), "1292052"); err != nil {
		t.Fatal(err)
	}
	if gatewayCalls.Load() != 1 {
		t.Fatalf("gateway calls = %d, want 1", gatewayCalls.Load())
	}
	if !strings.Contains(embedded, "越狱") || !strings.Contains(embedded, "肖申克的救赎") {
		t.Fatalf("AI 失败后应退回元数据，实际 = %q", embedded)
	}
}

// AI 每次改写的措辞都不同。语义哈希必须只覆盖元数据，否则 worker 每轮都会
// 判定「内容变了」而无限重算，把 AI Gateway 的额度烧光。
func TestEmbeddingServiceHashesMetadataNotAIOutput(t *testing.T) {
	var gatewayCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/chat/completions" {
			gatewayCalls.Add(1)
			encoded, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{
					"content": "每次都不一样的改写 " + strconv.Itoa(int(gatewayCalls.Load())),
				}}},
			})
			return testJSONResponse(request, http.StatusOK, string(encoded)), nil
		}
		vector := make([]float32, embeddingDimensions)
		encoded, _ := json.Marshal(map[string]any{"embedding": vector})
		return testJSONResponse(request, http.StatusOK, string(encoded)), nil
	})}

	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1", Title: "标题", Summary: "简介"})
	markEmbeddingMetadataReady(t, store, "1")
	service := NewEmbeddingService(client, store, EmbeddingConfig{
		OllamaHost: "https://ollama.test", CFGatewayURL: "https://gateway.test", CFAPIToken: "cf-token",
	})
	for range 3 {
		if err := service.Enrich(t.Context(), "1"); err != nil {
			t.Fatal(err)
		}
	}
	if gatewayCalls.Load() != 1 {
		t.Fatalf("gateway calls = %d, want 1（元数据没变就不该重算）", gatewayCalls.Load())
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
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1", Title: "标题", Summary: "简介"})
	markEmbeddingMetadataReady(t, store, "1")
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

func TestEmbeddingServiceRejectsIncompleteMetadataBeforeCallingModels(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return testJSONResponse(nil, http.StatusInternalServerError, `{}`), nil
	})}
	store := &embeddingStoreStub{movie: &Movie{
		DoubanID: "1292052", Title: "只有标题", MetadataStatus: "partial", CompletenessScore: 15,
	}}
	service := NewEmbeddingService(client, store, EmbeddingConfig{
		OllamaHost: "https://ollama.test", CFGatewayURL: "https://gateway.test", CFAPIToken: "cf-token",
	})
	if err := service.Enrich(t.Context(), "1292052"); err == nil || !strings.Contains(err.Error(), "metadata incomplete") {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("model calls = %d, want 0", calls.Load())
	}
}

func TestEmbeddingUpToDateRequiresCurrentMetadataHashAndDimensions(t *testing.T) {
	movie := Movie{
		DoubanID: "1292052", Title: "标题", Summary: "简介",
		MetadataStatus: "ready", CompletenessScore: 70,
		Embedding: make([]float32, embeddingDimensions),
	}
	movie.EmbeddingSemanticHash = contentHash(strings.TrimSpace(embeddingInput(movie)))
	if !embeddingMetadataComplete(&movie) || !embeddingUpToDate(&movie) {
		t.Fatal("完整资料的当前向量应该被识别为已完成")
	}
	movie.Summary = "更新后的简介"
	if embeddingUpToDate(&movie) {
		t.Fatal("元数据变化后不应复用旧向量")
	}
}

type embeddingStoreStub struct {
	Store
	movie *Movie
}

func (store *embeddingStoreStub) FindByDoubanID(context.Context, string) (*Movie, error) {
	return store.movie, nil
}

func markEmbeddingMetadataReady(t *testing.T, store *PostgresStore, doubanID string) {
	t.Helper()
	if _, err := store.database.Exec(t.Context(),
		`UPDATE media SET metadata_status = 'ready', completeness_score = 70 WHERE douban_id = $1`, doubanID); err != nil {
		t.Fatal(err)
	}
}
