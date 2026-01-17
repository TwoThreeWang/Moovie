package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// EmbeddingRequest Ollama embedding API 请求结构
type EmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// EmbeddingResponse Ollama embedding API 响应结构
type EmbeddingResponse struct {
	Embedding []float32 `json:"embedding"`
}

// GenerateEmbedding 调用本地 Ollama API 生成向量
func GenerateEmbedding(text string) ([]float32, error) {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "quentinz/bge-base-zh-v1.5"
	}

	reqBody := EmbeddingRequest{
		Model:  model,
		Prompt: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %v", err)
	}

	resp, err := http.Post(fmt.Sprintf("%s/api/embeddings", host), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("post request to ollama failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned error status: %d", resp.StatusCode)
	}

	var result EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response failed: %v", err)
	}

	return result.Embedding, nil
}
