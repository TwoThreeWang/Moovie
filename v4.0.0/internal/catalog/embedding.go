package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/sync/singleflight"
)

const embeddingDimensions = 768

type EmbeddingConfig struct {
	OllamaHost  string
	OllamaModel string
}

type EmbeddingService struct {
	client *http.Client
	store  Store
	config EmbeddingConfig
	group  singleflight.Group
}

func NewEmbeddingService(client *http.Client, store Store, cfg EmbeddingConfig) *EmbeddingService {
	cfg.OllamaHost = strings.TrimRight(cfg.OllamaHost, "/")
	if cfg.OllamaHost == "" {
		cfg.OllamaHost = "http://localhost:11434"
	}
	if cfg.OllamaModel == "" {
		cfg.OllamaModel = "quentinz/bge-base-zh-v1.5"
	}
	return &EmbeddingService{client: client, store: store, config: cfg}
}

func (service *EmbeddingService) Enrich(ctx context.Context, doubanID string) error {
	_, err, _ := service.group.Do(doubanID, func() (any, error) {
		return nil, service.enrich(ctx, doubanID)
	})
	return err
}

func (service *EmbeddingService) enrich(ctx context.Context, doubanID string) error {
	movie, err := service.store.FindByDoubanID(ctx, doubanID)
	if err != nil {
		return fmt.Errorf("find movie for embedding: %w", err)
	}
	if movie == nil {
		return fmt.Errorf("movie not found: %s", doubanID)
	}
	content := truncateRunes(strings.TrimSpace(embeddingInput(*movie)), 1000)
	semanticHash := contentHash(content)
	if movie.EmbeddingSemanticHash == semanticHash && len(movie.Embedding) == embeddingDimensions {
		return nil
	}
	vector, err := service.generateVector(ctx, content)
	if err != nil {
		return err
	}
	if len(vector) != embeddingDimensions {
		return fmt.Errorf("embedding dimension mismatch: want %d, got %d", embeddingDimensions, len(vector))
	}
	if err := service.store.UpdateEmbedding(ctx, doubanID, content, semanticHash, vector); err != nil {
		return fmt.Errorf("persist embedding: %w", err)
	}
	return nil
}

func embeddingInput(movie Movie) string {
	directors := peopleNames(movie.Directors, 0)
	actors := peopleNames(movie.Actors, 5)
	return fmt.Sprintf("标题：%s\n原名：%s\n年份：%s\n地区：%s\n类型：%s\n导演：%s\n主演：%s\n剧情：%s",
		movie.Title, movie.OriginalTitle, movie.Year, movie.Countries, movie.Genres,
		strings.Join(directors, ","), strings.Join(actors, ","), movie.Summary)
}

func contentHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func peopleNames(encoded string, limit int) []string {
	var people []Director
	if json.Unmarshal([]byte(encoded), &people) != nil {
		return nil
	}
	names := make([]string, 0, len(people))
	for _, person := range people {
		if limit > 0 && len(names) >= limit {
			break
		}
		if name := strings.TrimSpace(person.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (service *EmbeddingService) generateVector(ctx context.Context, content string) ([]float32, error) {
	payload := struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}{Model: service.config.OllamaModel, Prompt: content}
	var response struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := service.postJSON(ctx, service.config.OllamaHost+"/api/embeddings", payload, &response); err != nil {
		return nil, fmt.Errorf("generate Ollama embedding: %w", err)
	}
	return response.Embedding, nil
}

func (service *EmbeddingService) postJSON(ctx context.Context, endpoint string, payload any, destination any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
