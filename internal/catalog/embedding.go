package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

// embeddingDimensions 是向量维度，必须和数据库里 vector 列的维度一致。
const embeddingDimensions = 768

// bge-base-zh-v1.5 的最大序列长度是 512 token，中文大约一字一 token。超出部分会被
// 模型静默丢弃，所以送进去的文本必须留足余量，不能按字符数随手截。
const maxEmbeddingRunes = 450

// EmbeddingConfig 是向量生成的配置：Ollama 是必需的，AI Gateway 是可选的语义改写层。
type EmbeddingConfig struct {
	OllamaHost  string
	OllamaModel string
	// AI Gateway 可选。配置后先把元数据改写成高语义密度的短描述再做 embedding，
	// 向量会更贴主题气质而不是演员和类型标签；未配置或调用失败时退回元数据本身。
	CFGatewayURL string
	CFAPIToken   string
	CFAIModel    string
}

// EmbeddingService 给影片生成语义向量，供「相似推荐」和「猜你喜欢」使用。
// 流程：元数据 →（可选）AI 改写成高密度描述 → Ollama 生成 768 维向量 → 写回 media.embedding。
type EmbeddingService struct {
	client *http.Client
	// aiClient 专供 AI Gateway。它必须和抓取用的 Client 分开：后者的超时是按搜索源
	// 配的（默认 10 秒），而一次非流式 chat completion 几乎不可能在 10 秒内返回响应头，
	// 共用等于让语义改写「必然超时」，重试三次也全是徒劳。
	aiClient *http.Client
	store    Store
	config   EmbeddingConfig
	group    singleflight.Group
	// AI Gateway 偶发 429 和超时是常态，退避重试后才退回元数据。
	// 单独抽成字段是为了让测试能把等待清零，不必真的睡十几秒。
	retryDelays []time.Duration
}

// EmbeddingOption 是向量服务的可选装配项。
type EmbeddingOption func(*EmbeddingService)

// WithEmbeddingAIClient 指定调用 AI Gateway 的 Client，超时应当按 LLM 的响应时间配置。
func WithEmbeddingAIClient(client *http.Client) EmbeddingOption {
	return func(service *EmbeddingService) {
		if client != nil {
			service.aiClient = client
		}
	}
}

// NewEmbeddingService 创建向量服务，未配置时默认连本机 Ollama。
func NewEmbeddingService(client *http.Client, store Store, cfg EmbeddingConfig, options ...EmbeddingOption) *EmbeddingService {
	cfg.OllamaHost = strings.TrimRight(cfg.OllamaHost, "/")
	if cfg.OllamaHost == "" {
		cfg.OllamaHost = "http://localhost:11434"
	}
	if cfg.OllamaModel == "" {
		cfg.OllamaModel = "quentinz/bge-base-zh-v1.5"
	}
	service := &EmbeddingService{client: client, aiClient: client, store: store, config: cfg,
		retryDelays: []time.Duration{3 * time.Second, 5 * time.Second, 8 * time.Second}}
	for _, option := range options {
		option(service)
	}
	return service
}

// Enrich 为一部影片生成向量，同一条目的并发调用会被合并成一次。
func (service *EmbeddingService) Enrich(ctx context.Context, doubanID string) error {
	_, err, _ := service.group.Do(doubanID, func() (any, error) {
		return nil, service.enrich(ctx, doubanID)
	})
	return err
}

// enrich 为一部影片生成语义向量。元数据是否变化由上游（updateRefreshState）判断，
// 这里只管拿到当前元数据、调 AI 改写、生成向量、写回。
func (service *EmbeddingService) enrich(ctx context.Context, doubanID string) error {
	movie, err := service.store.FindByDoubanID(ctx, doubanID)
	if err != nil {
		return fmt.Errorf("find movie for embedding: %w", err)
	}
	if movie == nil {
		return fmt.Errorf("movie not found: %s", doubanID)
	}
	if !embeddingMetadataComplete(movie) {
		return fmt.Errorf("movie metadata incomplete for embedding: %s (status=%s, completeness=%d)",
			doubanID, movie.MetadataStatus, movie.CompletenessScore)
	}
	metadata := strings.TrimSpace(embeddingInput(*movie))
	content := service.semanticContent(ctx, *movie, metadata)
	vector, err := service.generateVector(ctx, content)
	if err != nil {
		return err
	}
	if len(vector) != embeddingDimensions {
		return fmt.Errorf("embedding dimension mismatch: want %d, got %d", embeddingDimensions, len(vector))
	}
	if err := service.store.UpdateEmbedding(ctx, doubanID, content, vector); err != nil {
		return fmt.Errorf("persist embedding: %w", err)
	}
	return nil
}

// embeddingMetadataComplete 同时检查“基础资料已就绪”和“完整度达到 70”。
// 两项是独立条件：分数达标不代表 metadata_status 一定不是 partial。
func embeddingMetadataComplete(movie *Movie) bool {
	return movie != nil && movie.MetadataStatus != "partial" && movie.CompletenessScore >= 70
}

// embeddingInput 是发给 AI 改写层的规范元数据文本。
// semantic_hash 在 updateRefreshState 中直接对对应的规范字段取哈希，不包含 AI 输出。
// 它不直接送进向量模型，所以不受 512 token 限制。
func embeddingInput(movie Movie) string {
	directors := peopleNames(movie.Directors, 0)
	actors := peopleNames(movie.Actors, 5)
	return fmt.Sprintf("标题：%s\n原名：%s\n年份：%s\n地区：%s\n类型：%s\n导演：%s\n主演：%s\n剧情：%s",
		movie.Title, movie.OriginalTitle, movie.Year, movie.Countries, movie.Genres,
		strings.Join(directors, ","), strings.Join(actors, ","), movie.Summary)
}

// fallbackContent 在没有 AI Gateway 或调用失败时使用。
//
// 相比 embeddingInput 做了三处调整，都是为了在 512 token 窗口里塞进最多语义：
// 剧情提到最前面（原来排在最后，长简介会被模型截掉）、去掉每篇都一样的字段标签、
// 主演只留 3 个（人名是高熵噪音，会把相似度拉向「同演员」而不是「同题材」）。
func fallbackContent(movie Movie) string {
	parts := make([]string, 0, 4)
	if summary := strings.TrimSpace(movie.Summary); summary != "" {
		parts = append(parts, summary)
	}
	parts = append(parts, strings.TrimSpace(movie.Title+" "+movie.OriginalTitle))
	if descriptor := joinNonEmpty([]string{movie.Year, movie.Countries, movie.Genres}, " "); descriptor != "" {
		parts = append(parts, descriptor)
	}
	people := append(peopleNames(movie.Directors, 2), peopleNames(movie.Actors, 3)...)
	if len(people) > 0 {
		parts = append(parts, strings.Join(people, " "))
	}
	return truncateRunes(strings.Join(parts, "\n"), maxEmbeddingRunes)
}

// joinNonEmpty 拼接非空片段。
func joinNonEmpty(values []string, separator string) string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, separator)
}

// semanticContent 返回真正送进向量模型的文本。
func (service *EmbeddingService) semanticContent(ctx context.Context, movie Movie, metadata string) string {
	if service.config.CFGatewayURL == "" || service.config.CFAPIToken == "" {
		return fallbackContent(movie)
	}
	delays := service.retryDelays
	for attempt := 0; ; attempt++ {
		summary, err := service.generateSemanticSummary(ctx, metadata)
		if err == nil {
			if trimmed := strings.TrimSpace(summary); trimmed != "" {
				return truncateRunes(trimmed, maxEmbeddingRunes)
			}
			err = fmt.Errorf("AI 返回空内容")
		}
		if attempt >= len(delays) {
			slog.Warn("AI 语义描述生成失败，改用元数据", "douban_id", movie.DoubanID, "error", err)
			return fallbackContent(movie)
		}
		slog.Warn("AI 语义描述生成失败，稍后重试",
			"douban_id", movie.DoubanID, "attempt", attempt+1, "retry_in", delays[attempt], "error", err)
		select {
		case <-ctx.Done():
			return fallbackContent(movie)
		case <-time.After(delays[attempt]):
		}
	}
}

// semanticPrompt 是让大模型把影片元数据改写成一段高密度描述的提示词，
// 改写结果再交给向量模型，比直接拿简介做向量效果好。
const semanticPrompt = `**Role:** 你是一位精通影视社会学和推荐算法的专家。你的任务是将碎片化的电影元数据重构成一段**极高语义密度的描述文本**，专供向量嵌入（Embedding）模型使用。

**Task:** 请根据提供的原始数据，撰写一段 150-200 字的语义特征文本。

**Input Data:**
%s

**Writing Guidelines (核心要求):**

1. **元数据叙事化与加权:** 不要使用"导演：xxx"这种列表格式。请将导演的执导风格、演员的演技标签融入叙事，禁止使用"这部电影是由..."等废话引导。
2. **特征压缩（Feature Density）:** 放弃长难句，使用"名词+形容词"的短句堆叠。将剧情摘要转化为核心冲突与母题（Trope）。例如：不写"讲述了抢劫的过程"，写"聚焦银行抢劫、高智商博弈与团队救赎"。
3. **提取深层语义标签:** 深入挖掘摘要，自然地融合电影的主题、艺术风格、核心冲突，提取隐含的**电影冲突（Conflict）**和**母题（Trope）**。例如：不要只写"抢劫"，要写"密闭空间压力、高智商博弈、反英雄主义、社会阶级对抗"。
4. **视听风格建模:** 精炼描述视听风格。使用诸如"冷峻、压抑、快节奏剪辑、迷幻视觉、黑色电影叙事"等具有强区分度的风格词。
5. **系列与关联处理:** 如果是续集或系列剧，请明确标注其在系列中的地位（如：系列序章、高潮转折），并强调其核心宇宙的共性，以便同系列作品在向量空间聚类。
6. **推荐理由预埋:** 结尾必须包含"该片适合喜欢 [具体风格/作品/导演] 的观众"，模拟用户的搜索意图，增强匹配权重。

**Output Format:**
以"《电影标题》(英文名/别名)"开头输出为一段连贯、无标题、无列表的自然语言段落，字数严格控制在 200 字以内。`

// generateSemanticSummary 调用 OpenAI 兼容的 Cloudflare AI Gateway。
func (service *EmbeddingService) generateSemanticSummary(ctx context.Context, metadata string) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}{
		Model: service.config.CFAIModel,
		Messages: []message{
			{Role: "system", Content: "你是一个专业的电影内容分析专家。"},
			{Role: "user", Content: fmt.Sprintf(semanticPrompt, metadata)},
		},
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := service.postJSON(ctx, service.aiClient, service.config.CFGatewayURL+"/chat/completions",
		payload, &response, service.config.CFAPIToken); err != nil {
		return "", err
	}
	if response.Error != nil {
		return "", fmt.Errorf("AI Gateway 返回错误: %s", response.Error.Message)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("AI Gateway 未返回内容")
	}
	return response.Choices[0].Message.Content, nil
}

// peopleNames 从导演/演员 JSON 里取人名，limit<=0 表示不限。
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

// generateVector 调 Ollama 生成向量。
func (service *EmbeddingService) generateVector(ctx context.Context, content string) ([]float32, error) {
	payload := struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}{Model: service.config.OllamaModel, Prompt: content}
	var response struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := service.postJSON(ctx, service.client, service.config.OllamaHost+"/api/embeddings", payload, &response, ""); err != nil {
		return nil, fmt.Errorf("generate Ollama embedding: %w", err)
	}
	return response.Embedding, nil
}

// postJSON 是 Ollama 和 AI Gateway 共用的 POST 封装。
func (service *EmbeddingService) postJSON(ctx context.Context, client *http.Client, endpoint string, payload, destination any, bearerToken string) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if client == nil {
		client = service.client
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return classifyUpstreamStatus("upstream", response)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// truncateRunes 按字符数截断，避免把汉字截半。
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
