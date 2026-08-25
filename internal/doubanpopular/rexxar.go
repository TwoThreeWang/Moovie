// Package doubanpopular 抓取豆瓣手机版（Rexxar）的热门榜单，用于首页热门推荐。
// 这是非公开接口，需要伪装成手机浏览器并带上随机 bid Cookie，随时可能失效，
// 所以调用方必须能容忍它返回错误（见 playback 的热门榜多级兜底）。
package doubanpopular

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Subject 是榜单里的一个条目。
type Subject struct {
	ID           string
	Title        string
	Rate         string
	Cover        string
	URL          string
	EpisodesInfo string
}

// item 是豆瓣接口的原始返回结构。
type item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Pic   struct {
		Normal string `json:"normal"`
		Large  string `json:"large"`
	} `json:"pic"`
	Rating struct {
		Value float64 `json:"value"`
	} `json:"rating"`
	EpisodesInfo string `json:"episodes_info"`
}

// FetchRexxar 抓取指定类型的热门榜。
// 电视剧要请求国产剧和美剧两个榜单，其中一个失败仍返回另一个的结果。
func FetchRexxar(ctx context.Context, client *http.Client, mediaType string) ([]Subject, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoints, collection, err := rexxarEndpoints(mediaType)
	if err != nil {
		return nil, err
	}
	items := make([]item, 0, 50)
	var lastErr error
	for _, endpoint := range endpoints {
		fetched, fetchErr := fetch(ctx, client, endpoint, collection)
		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}
		items = append(items, fetched...)
	}
	if len(items) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("Rexxar returned no subjects")
		}
		return nil, lastErr
	}
	result := make([]Subject, 0, len(items))
	for _, value := range items {
		cover := value.Pic.Normal
		if cover == "" {
			cover = value.Pic.Large
		}
		result = append(result, Subject{
			ID: value.ID, Title: value.Title, Rate: fmt.Sprintf("%.1f", value.Rating.Value),
			Cover: cover, URL: "https://movie.douban.com/subject/" + url.PathEscape(value.ID) + "/", EpisodesInfo: value.EpisodesInfo,
		})
	}
	return result, nil
}

// rexxarEndpoints 返回该类型对应的接口地址，第二个返回值表示响应是不是「合集」格式。
func rexxarEndpoints(mediaType string) ([]string, bool, error) {
	const base = "https://m.douban.com/rexxar/api/v2/"
	switch mediaType {
	case "movie":
		return []string{base + "movie/hot_gaia?area=%E5%85%A8%E9%83%A8&sort=recommend&playable=0&loc_id=0&start=0&count=50&for_mobile=1"}, false, nil
	case "tv":
		return []string{
			base + "subject_collection/tv_domestic/items?items_only=1&start=0&count=25&for_mobile=1",
			base + "subject_collection/tv_american/items?items_only=1&start=0&count=25&for_mobile=1",
		}, true, nil
	case "show":
		return []string{base + "subject_collection/tv_variety_show/items?items_only=1&start=0&count=50&for_mobile=1"}, true, nil
	case "cartoon":
		return []string{base + "subject_collection/tv_animation/items?items_only=1&start=0&count=50&for_mobile=1"}, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported media type %q", mediaType)
	}
}

// fetch 请求单个榜单接口，两种响应格式的字段名不同要分开解析。
func fetch(ctx context.Context, client *http.Client, endpoint string, collection bool) ([]item, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
	request.Header.Set("Referer", "https://m.douban.com/")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cookie", "bid="+randomBid())
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Rexxar returned HTTP %d", response.StatusCode)
	}
	if collection {
		var payload struct {
			Items []item `json:"subject_collection_items"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return nil, fmt.Errorf("decode Rexxar collection: %w", err)
		}
		return payload.Items, nil
	}
	var payload struct {
		Items []item `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Rexxar movie list: %w", err)
	}
	return payload.Items, nil
}

// randomBid 生成随机 bid Cookie，豆瓣用它标识浏览器；固定值容易被风控。
func randomBid() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	buffer := make([]byte, 11)
	if _, err := rand.Read(buffer); err != nil {
		return "MoovieGuest"
	}
	for index := range buffer {
		buffer[index] = alphabet[int(buffer[index])%len(alphabet)]
	}
	return string(buffer)
}
