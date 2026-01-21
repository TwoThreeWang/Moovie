package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/user/moovie/internal/model"
)

// SourceCrawler 资源网爬虫接口
// 不做并发，只负责单站点请求，并发由调用方控制
type SourceCrawler interface {
	// Search 搜索视频，支持超时控制
	Search(ctx context.Context, baseUrl, keyword, sourceKey string, restrictedCategories []string) ([]model.VodItem, error)

	// GetDetail 获取视频详情
	GetDetail(ctx context.Context, baseUrl, vodId, sourceKey string) (*model.VodItem, error)
}

// DefaultSourceCrawler 默认资源网爬虫实现
type DefaultSourceCrawler struct {
	client  *http.Client
	timeout time.Duration
}

// NewSourceCrawler 创建资源网爬虫
func NewSourceCrawler(timeout time.Duration) *DefaultSourceCrawler {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &DefaultSourceCrawler{
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// vodApiResponse 资源网API响应结构
type vodApiResponse struct {
	Code      interface{}              `json:"code"`
	Msg       string                   `json:"msg"`
	Page      interface{}              `json:"page"`
	PageCount interface{}              `json:"pagecount"`
	Limit     interface{}              `json:"limit"`
	Total     interface{}              `json:"total"`
	List      []map[string]interface{} `json:"list"`
}

// Search 搜索视频
func (c *DefaultSourceCrawler) Search(ctx context.Context, baseUrl, keyword, sourceKey string, restrictedCategories []string) ([]model.VodItem, error) {
	// 构建搜索URL
	apiUrl := fmt.Sprintf("%s?ac=videolist&pg=1&wd=%s", baseUrl, url.QueryEscape(keyword))

	// 创建带上下文的请求
	req, err := http.NewRequestWithContext(ctx, "GET", apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// 发送请求
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求返回状态码: %d", resp.StatusCode)
	}

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 解析JSON
	var apiResp vodApiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	// 转换为VodItem列表，所有字段统一转为string
	var items []model.VodItem
	for _, item := range apiResp.List {
		vodItem := c.mapToVodItem(item, sourceKey)

		// 没有播放链接的不采集
		if vodItem.VodPlayUrl == "" {
			continue
		}

		// 分类过滤
		if len(restrictedCategories) > 0 {
			blocked := false
			for _, restricted := range restrictedCategories {
				if strings.Contains(vodItem.TypeName, restricted) {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
		}

		items = append(items, vodItem)
	}

	return items, nil
}

// GetDetail 获取视频详情
func (c *DefaultSourceCrawler) GetDetail(ctx context.Context, baseUrl, vodId, sourceKey string) (*model.VodItem, error) {
	// 构建详情URL
	apiUrl := fmt.Sprintf("%s?ac=detail&ids=%s", baseUrl, vodId)

	// 创建带上下文的请求
	req, err := http.NewRequestWithContext(ctx, "GET", apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// 发送请求
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求返回状态码: %d", resp.StatusCode)
	}

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 解析JSON
	var apiResp vodApiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	if len(apiResp.List) == 0 {
		return nil, nil
	}

	vodItem := c.mapToVodItem(apiResp.List[0], sourceKey)
	return &vodItem, nil
}

// mapToVodItem 将map转换为VodItem，所有字段统一转为string
func (c *DefaultSourceCrawler) mapToVodItem(item map[string]interface{}, sourceKey string) model.VodItem {
	return model.VodItem{
		SourceKey:   sourceKey,
		VodId:       toString(item["vod_id"]),
		VodName:     toString(item["vod_name"]),
		VodSub:      toString(item["vod_sub"]),
		VodEn:       toString(item["vod_en"]),
		VodTag:      toString(item["vod_tag"]),
		VodClass:    toString(item["vod_class"]),
		VodPic:      toString(item["vod_pic"]),
		VodActor:    toString(item["vod_actor"]),
		VodDirector: toString(item["vod_director"]),
		VodBlurb:    toString(item["vod_blurb"]),
		VodRemarks:  toString(item["vod_remarks"]),
		VodPubdate:  toString(item["vod_pubdate"]),
		VodTotal:    toString(item["vod_total"]),
		VodSerial:   toString(item["vod_serial"]),
		VodArea:     toString(item["vod_area"]),
		VodLang:     toString(item["vod_lang"]),
		VodYear:     toString(item["vod_year"]),
		VodDuration: toString(item["vod_duration"]),
		VodTime:     toString(item["vod_time"]),
		VodDoubanId: toString(item["vod_douban_id"]),
		VodContent:  toString(item["vod_content"]),
		VodPlayUrl:  toString(item["vod_play_url"]),
		TypeName:    toString(item["type_name"]),
	}
}

// toString 将任意类型转换为string
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		// JSON数字默认解析为float64
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%v", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", val)
	}
}
