package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
)

const crawlerUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
const maxAppleCMSResponseBytes = 4 << 20

type AppleCMSCrawler struct {
	client *http.Client
}

func NewAppleCMSCrawler(client *http.Client) *AppleCMSCrawler {
	if client == nil {
		client = http.DefaultClient
	}
	return &AppleCMSCrawler{client: client}
}

type appleCMSResponse struct {
	List []map[string]any `json:"list"`
}

func (crawler *AppleCMSCrawler) Search(ctx context.Context, baseURL, keyword, sourceKey string, restrictedCategories []string) ([]VodItem, error) {
	target := fmt.Sprintf("%s?ac=videolist&pg=1&wd=%s", baseURL, url.QueryEscape(keyword))
	if err := outbound.ValidatePublicHTTPURL(target); err != nil {
		return nil, fmt.Errorf("source endpoint is not public: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create source request: %w", err)
	}
	request.Header.Set("User-Agent", crawlerUserAgent)

	response, err := outbound.PublicRedirectClient(crawler.client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("request source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned status %d", response.StatusCode)
	}
	var payload appleCMSResponse
	if err := decodeAppleCMSResponse(response.Body, &payload); err != nil {
		return nil, fmt.Errorf("decode source response: %w", err)
	}

	items := make([]VodItem, 0, len(payload.List))
	for _, raw := range payload.List {
		item := mapAppleCMSItem(raw, sourceKey)
		if item.VodPlayUrl == "" || categoryBlocked(item.TypeName, restrictedCategories) {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// GetDetail 保留播放页和 TVBox 使用的 AppleCMS v10 详情请求。
// 详情请求刻意不经过搜索熔断过滤，因为用户已经选择具体来源，应该直接尝试一次。
func (crawler *AppleCMSCrawler) GetDetail(ctx context.Context, baseURL, vodID, sourceKey string) (*VodItem, error) {
	target := fmt.Sprintf("%s?ac=detail&ids=%s", baseURL, url.QueryEscape(vodID))
	if err := outbound.ValidatePublicHTTPURL(target); err != nil {
		return nil, fmt.Errorf("detail endpoint is not public: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create detail request: %w", err)
	}
	request.Header.Set("User-Agent", crawlerUserAgent)

	response, err := outbound.PublicRedirectClient(crawler.client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("request detail: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("detail source returned status %d", response.StatusCode)
	}
	var payload appleCMSResponse
	if err := decodeAppleCMSResponse(response.Body, &payload); err != nil {
		return nil, fmt.Errorf("decode detail response: %w", err)
	}
	if len(payload.List) == 0 {
		return nil, fmt.Errorf("detail source returned no video")
	}
	item := mapAppleCMSItem(payload.List[0], sourceKey)
	if item.VodPlayUrl == "" {
		return nil, fmt.Errorf("detail source returned no playback URL")
	}
	return &item, nil
}

func decodeAppleCMSResponse(reader io.Reader, destination *appleCMSResponse) error {
	limited := &io.LimitedReader{R: reader, N: maxAppleCMSResponseBytes + 1}
	if err := json.NewDecoder(limited).Decode(destination); err != nil {
		if limited.N <= 0 {
			return fmt.Errorf("source response exceeds %d bytes", maxAppleCMSResponseBytes)
		}
		return err
	}
	if limited.N <= 0 {
		return fmt.Errorf("source response exceeds %d bytes", maxAppleCMSResponseBytes)
	}
	return nil
}

func categoryBlocked(typeName string, restricted []string) bool {
	for _, keyword := range restricted {
		if strings.Contains(typeName, keyword) {
			return true
		}
	}
	return false
}

func mapAppleCMSItem(item map[string]any, sourceKey string) VodItem {
	return VodItem{
		SourceKey:   sourceKey,
		VodId:       stringify(item["vod_id"]),
		VodName:     stringify(item["vod_name"]),
		VodSub:      stringify(item["vod_sub"]),
		VodEn:       stringify(item["vod_en"]),
		VodTag:      stringify(item["vod_tag"]),
		VodClass:    stringify(item["vod_class"]),
		VodPic:      stringify(item["vod_pic"]),
		VodActor:    stringify(item["vod_actor"]),
		VodDirector: stringify(item["vod_director"]),
		VodBlurb:    stringify(item["vod_blurb"]),
		VodRemarks:  stringify(item["vod_remarks"]),
		VodPubdate:  stringify(item["vod_pubdate"]),
		VodTotal:    stringify(item["vod_total"]),
		VodSerial:   stringify(item["vod_serial"]),
		VodArea:     stringify(item["vod_area"]),
		VodLang:     stringify(item["vod_lang"]),
		VodYear:     stringify(item["vod_year"]),
		VodDuration: stringify(item["vod_duration"]),
		VodTime:     stringify(item["vod_time"]),
		VodDoubanId: stringify(item["vod_douban_id"]),
		VodContent:  stringify(item["vod_content"]),
		VodPlayUrl:  stringify(item["vod_play_url"]),
		TypeName:    stringify(item["type_name"]),
	}
}

func stringify(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", typed)
	}
}
