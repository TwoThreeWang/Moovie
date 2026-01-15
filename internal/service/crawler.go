package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
)

// Crawler 豆瓣爬虫服务
type Crawler struct {
	movieRepo *repository.MovieRepository
	client    *http.Client
}

// NewCrawler 创建爬虫服务
func NewCrawler(movieRepo *repository.MovieRepository) *Crawler {
	return &Crawler{
		movieRepo: movieRepo,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// CrawlDoubanMovie 爬取豆瓣电影详情页
func (c *Crawler) CrawlDoubanMovie(doubanID string) error {
	url := fmt.Sprintf("https://movie.douban.com/subject/%s/", doubanID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头，模拟浏览器
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://movie.douban.com/")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("请求返回状态码: %d", resp.StatusCode)
	}

	// 解析 HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("解析 HTML 失败: %w", err)
	}

	// 提取电影信息
	movie := c.parseMoviePage(doc, doubanID)

	// 保存到数据库
	if err := c.movieRepo.Upsert(movie); err != nil {
		return fmt.Errorf("保存电影失败: %w", err)
	}

	log.Printf("[爬虫] 成功爬取电影: %s (豆瓣ID: %s)", movie.Title, doubanID)
	return nil
}

// parseMoviePage 解析电影详情页
func (c *Crawler) parseMoviePage(doc *goquery.Document, doubanID string) *model.Movie {
	movie := &model.Movie{
		DoubanID: doubanID,
	}

	// 标题
	movie.Title = strings.TrimSpace(doc.Find("h1 span[property='v:itemreviewed']").Text())

	// 年份
	yearText := doc.Find("h1 .year").Text()
	movie.Year = strings.Trim(yearText, "()")

	// 原标题 - 从 info 区域提取
	infoText := doc.Find("#info").Text()
	if match := regexp.MustCompile(`又名:\s*(.+)`).FindStringSubmatch(infoText); len(match) > 1 {
		movie.OriginalTitle = strings.TrimSpace(strings.Split(match[1], "/")[0])
	}

	// 海报
	posterImg := doc.Find("#mainpic img")
	if poster, exists := posterImg.Attr("src"); exists {
		movie.Poster = poster
	}

	// 评分
	ratingText := doc.Find("strong.rating_num").Text()
	if ratingText != "" {
		fmt.Sscanf(ratingText, "%f", &movie.Rating)
	}

	// 类型
	doc.Find("span[property='v:genre']").Each(func(i int, s *goquery.Selection) {
		movie.Genres = append(movie.Genres, strings.TrimSpace(s.Text()))
	})

	// 国家/地区
	if match := regexp.MustCompile(`制片国家/地区:\s*(.+)`).FindStringSubmatch(infoText); len(match) > 1 {
		countries := strings.Split(match[1], "/")
		for _, country := range countries {
			movie.Countries = append(movie.Countries, strings.TrimSpace(country))
		}
	}

	// 片长
	movie.Duration = strings.TrimSpace(doc.Find("span[property='v:runtime']").Text())

	// 导演
	doc.Find("a[rel='v:directedBy']").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		// 从链接提取人物 ID: /celebrity/1234/
		personID := ""
		if match := regexp.MustCompile(`/celebrity/(\d+)/`).FindStringSubmatch(href); len(match) > 1 {
			personID = match[1]
		}
		movie.Directors = append(movie.Directors, model.Person{
			ID:   personID,
			Name: strings.TrimSpace(s.Text()),
		})
	})

	// 主演
	doc.Find("a[rel='v:starring']").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		personID := ""
		if match := regexp.MustCompile(`/celebrity/(\d+)/`).FindStringSubmatch(href); len(match) > 1 {
			personID = match[1]
		}
		movie.Actors = append(movie.Actors, model.Person{
			ID:   personID,
			Name: strings.TrimSpace(s.Text()),
		})
	})

	// 剧情简介
	summary := doc.Find("span[property='v:summary']").Text()
	movie.Summary = strings.TrimSpace(summary)

	// IMDb ID
	if match := regexp.MustCompile(`IMDb:\s*(tt\d+)`).FindStringSubmatch(infoText); len(match) > 1 {
		movie.IMDbID = match[1]
	}

	// 尝试从 JSON-LD 获取更多信息
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		var ldData map[string]interface{}
		if err := json.Unmarshal([]byte(s.Text()), &ldData); err == nil {
			// 如果标题为空，从 JSON-LD 获取
			if movie.Title == "" {
				if name, ok := ldData["name"].(string); ok {
					movie.Title = name
				}
			}
		}
	})

	return movie
}

// CrawlAsync 异步爬取电影信息
func (c *Crawler) CrawlAsync(doubanID string) {
	go func() {
		if err := c.CrawlDoubanMovie(doubanID); err != nil {
			log.Printf("[爬虫] 爬取失败 (豆瓣ID: %s): %v", doubanID, err)
		}
	}()
}
