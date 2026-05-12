package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// OpenAIRequest OpenAI 兼容的请求结构
type OpenAIRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
}

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIResponse OpenAI 兼容的响应结构
type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// GenerateCloudflareAISummary 调用 Cloudflare AI Gateway (OpenAI 兼容) 生成描述
func GenerateCloudflareAISummary(gatewayURL, apiToken, model, prompt string) (string, error) {
	if gatewayURL == "" || apiToken == "" {
		return "", fmt.Errorf("Cloudflare AI Gateway 配置不完整")
	}

	// 构造 OpenAI 兼容的消息格式
	reqBody := OpenAIRequest{
		Model: model,
		Messages: []OpenAIMessage{
			{Role: "system", Content: "你是一个专业的电影内容分析专家。"},
			{Role: "user", Content: prompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request failed: %v", err)
	}

	client := GlobalHttpClient

	req, err := http.NewRequest("POST", gatewayURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request failed: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("post request to cloudflare ai failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cloudflare ai returned status: %d", resp.StatusCode)
	}

	var result OpenAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response failed: %v", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("cloudflare ai error: %s", result.Error.Message)
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("cloudflare ai returned no content")
}
