package douban

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// 豆瓣的两个域名：手机版接口和 RSS。
const (
	defaultRexxarBase = "https://m.douban.com"
	defaultRSSBase    = "https://www.douban.com"
)

// Client 是豆瓣接口客户端，同样用的是手机版非公开接口。
type Client struct {
	httpClient *http.Client
	rexxarBase string
	rssBase    string
}

// ClientOption 用于测试时替换域名。
type ClientOption func(*Client)

// WithBases 覆盖接口域名。
func WithBases(rexxarBase, rssBase string) ClientOption {
	return func(client *Client) {
		client.rexxarBase = strings.TrimRight(rexxarBase, "/")
		client.rssBase = strings.TrimRight(rssBase, "/")
	}
}

// NewClient 创建豆瓣客户端。
func NewClient(httpClient *http.Client, options ...ClientOption) *Client {
	client := &Client{httpClient: httpClient, rexxarBase: defaultRexxarBase, rssBase: defaultRSSBase}
	for _, option := range options {
		option(client)
	}
	return client
}

// interestsResponse 是标记列表接口的返回。
type interestsResponse struct {
	Total     int        `json:"total"`
	Interests []Interest `json:"interests"`
}

// Interest 是一条豆瓣标记（想看或看过，可能带评分和短评）。
type Interest struct {
	Status     string `json:"status"`
	Comment    string `json:"comment"`
	CreateTime string `json:"create_time"`
	Rating     *struct {
		Value float64 `json:"value"`
	} `json:"rating"`
	Subject Subject `json:"subject"`
}

// Subject 是标记指向的条目。
type Subject struct {
	ID       json.Number `json:"id"`
	Title    string      `json:"title"`
	Type     string      `json:"type"`
	Subtype  string      `json:"subtype"`
	Year     string      `json:"year"`
	CoverURL string      `json:"cover_url"`
	Pic      struct {
		Large string `json:"large"`
	} `json:"pic"`
}

// ValidateUser 用取一条标记的方式验证豆瓣 ID 是否存在且资料公开。
func (client *Client) ValidateUser(ctx context.Context, doubanUserID string) error {
	_, _, err := client.Interests(ctx, doubanUserID, "movie", "mark", 0, 1)
	return err
}

// Interests 分页读取用户的标记列表，返回本页数据和总数。
func (client *Client) Interests(ctx context.Context, doubanUserID, itemType, status string, start, count int) ([]Interest, int, error) {
	endpoint := fmt.Sprintf("%s/rexxar/api/v2/user/%s/interests?type=%s&status=%s&start=%d&count=%d&ck=&for_mobile=1",
		client.rexxarBase, url.PathEscape(doubanUserID), url.QueryEscape(itemType), url.QueryEscape(status), start, count)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
	request.Header.Set("Referer", "https://m.douban.com/")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cookie", `ll="108288"; bid=`+randomBid())
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, 0, fmt.Errorf("豆瓣用户不存在")
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, 0, fmt.Errorf("豆瓣返回状态码 %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result interestsResponse
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("解析豆瓣响应失败: %w", err)
	}
	return result.Interests, result.Total, nil
}

// RSSSubjects 读取用户 RSS，返回最近变动的条目 ID 集合和其中最早的时间。
// 增量同步靠它决定翻到哪一页可以停。
func (client *Client) RSSSubjects(ctx context.Context, doubanUserID string) (map[string]bool, time.Time, error) {
	endpoint := client.rssBase + "/feed/people/" + url.PathEscape(doubanUserID) + "/interests"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	request.Header.Set("Accept", "application/rss+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Referer", "https://www.douban.com/")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("请求豆瓣 RSS 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, time.Time{}, fmt.Errorf("豆瓣 RSS 返回状态码 %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var feed struct {
		Channel struct {
			Items []struct {
				Link    string `xml:"link"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.NewDecoder(response.Body).Decode(&feed); err != nil {
		return nil, time.Time{}, fmt.Errorf("解析豆瓣 RSS 失败: %w", err)
	}
	subjects := make(map[string]bool)
	var earliest time.Time
	for _, item := range feed.Channel.Items {
		id := extractSubjectID(item.Link)
		if id == "" {
			continue
		}
		subjects[id] = true
		published, _ := time.Parse(time.RFC1123, item.PubDate)
		if published.IsZero() {
			published, _ = time.Parse(time.RFC1123Z, item.PubDate)
		}
		if !published.IsZero() && (earliest.IsZero() || published.Before(earliest)) {
			earliest = published
		}
	}
	if !earliest.IsZero() {
		earliest = earliest.Add(-24 * time.Hour)
	}
	return subjects, earliest, nil
}

// subjectIDPattern 从豆瓣链接里提取条目 ID。
var subjectIDPattern = regexp.MustCompile(`/(?:subject|movie|tv)/(\d+)`)

// extractSubjectID 从链接提取条目 ID。
func extractSubjectID(link string) string {
	matches := subjectIDPattern.FindStringSubmatch(link)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// randomBid 生成随机 bid Cookie，降低被风控的概率。
func randomBid() string {
	buffer := make([]byte, 9)
	if _, err := rand.Read(buffer); err != nil {
		return "moovie-sync"
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
