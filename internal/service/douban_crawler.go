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
	"github.com/pgvector/pgvector-go"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
)

// DoubanMovieSuggest 豆瓣电影搜索建议
type DoubanMovieSuggest struct {
	Episode  string `json:"episode"`
	Img      string `json:"img"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Year     string `json:"year"`
	SubTitle string `json:"sub_title"`
	ID       string `json:"id"`
}

// MovieSuggestResponse 返回给前端的电影建议
type MovieSuggestResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SubTitle string `json:"sub_title"`
	Type     string `json:"type"`
	Year     string `json:"year"`
	Episode  string `json:"episode"`
	Img      string `json:"img"`
}

// DoubanPopularSubject 豆瓣热门电影/电视剧
type DoubanPopularSubject struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Rate         string `json:"rate"`
	Cover        string `json:"cover"`
	URL          string `json:"url"`
	IsNew        bool   `json:"is_new"`
	EpisodesInfo string `json:"episodes_info"`
}

// DoubanPopularResponse 豆瓣热门响应
type DoubanPopularResponse struct {
	Subjects []DoubanPopularSubject `json:"subjects"`
}

// Crawler 豆瓣爬虫服务
type DoubanCrawler struct {
	movieRepo *repository.MovieRepository
	client    *http.Client
}

// NewDoubanCrawler 创建爬虫服务
func NewDoubanCrawler(movieRepo *repository.MovieRepository) *DoubanCrawler {
	return &DoubanCrawler{
		movieRepo: movieRepo,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// CrawlDoubanMovie 爬取豆瓣电影详情页
func (c *DoubanCrawler) CrawlDoubanMovie(doubanID string) error {
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
	req.Header.Set("Cookie", `ll="108288"; bid=KbVLyVSe9PI; _pk_id.100001.4cf6=8bbc2ff56b7eadbf.1768570153.; _pk_ses.100001.4cf6=1; ap_v=0,6.0; __yadk_uid=Q9rDtuhs4wWFa5rwTvB6WpPxXFFRnScS; __utma=30149280.1349976959.1768570154.1768570154.1768570154.1; __utmb=30149280.0.10.1768570154; __utmc=30149280; __utmz=30149280.1768570154.1.1.utmcsr=(direct)|utmccn=(direct)|utmcmd=(none); __utma=223695111.369068564.1768570154.1768570154.1768570154.1; __utmb=223695111.0.10.1768570154; __utmc=223695111; __utmz=223695111.1768570154.1.1.utmcsr=(direct)|utmccn=(direct)|utmcmd=(none); _vwo_uuid_v2=D25CE55BF48CA21000B5BED858D7F40A3|a607d8885a8913b757f280679b32e9b1`)

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

	// 检测是否被重定向到验证页面
	pageTitle := doc.Find("title").Text()
	if pageTitle == "豆瓣" || strings.Contains(pageTitle, "验证") {
		return fmt.Errorf("触发豆瓣反爬验证 (豆瓣ID: %s)，请稍后重试", doubanID)
	}

	// 提取电影信息
	movie := c.parseMoviePage(doc, doubanID)

	// 强制校验：如果没有标题，视为抓取失败
	if movie.Title == "" {
		return fmt.Errorf("无法解析出电影标题 (豆瓣ID: %s)，页面可能结构变化或触发反爬", doubanID)
	}

	// 生成向量并增强电影信息
	if err := c.EnrichMovieWithVector(movie); err != nil {
		log.Printf("[爬虫] 向量生成失败 (豆瓣ID: %s): %v", doubanID, err)
		// 向量生成失败不影响核心流程，继续保存电影
	}

	// 保存到数据库
	if err := c.movieRepo.Upsert(movie); err != nil {
		return fmt.Errorf("保存电影失败: %w", err)
	}

	log.Printf("[爬虫] 成功爬取电影: %s (豆瓣ID: %s)", movie.Title, doubanID)
	return nil
}

// parseMoviePage 解析电影详情页
func (c *DoubanCrawler) parseMoviePage(doc *goquery.Document, doubanID string) *model.Movie {
	movie := &model.Movie{
		DoubanID: doubanID,
	}

	// 标题解析增强策略
	// 策略 1: property='v:itemreviewed'
	title := doc.Find("h1 span[property='v:itemreviewed']").Text()
	if title == "" {
		// 策略 2: h1 直接下的 span
		title = doc.Find("h1 span:first-child").Text()
	}
	if title == "" {
		// 策略 3: 去掉 .year 后的 h1 文本
		titleHeader := doc.Find("h1").Clone()
		titleHeader.Find(".year").Remove()
		title = titleHeader.Text()
	}
	movie.Title = strings.TrimSpace(title)

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
	var genres []string
	doc.Find("span[property='v:genre']").Each(func(i int, s *goquery.Selection) {
		genres = append(genres, strings.TrimSpace(s.Text()))
	})
	movie.Genres = strings.Join(genres, ",")

	// 国家/地区
	if match := regexp.MustCompile(`制片国家/地区:\s*(.+)`).FindStringSubmatch(infoText); len(match) > 1 {
		countries := strings.Split(match[1], "/")
		var countryList []string
		for _, country := range countries {
			countryList = append(countryList, strings.TrimSpace(country))
		}
		movie.Countries = strings.Join(countryList, ",")
	}

	// 片长
	movie.Duration = strings.TrimSpace(doc.Find("span[property='v:runtime']").Text())

	// 导演
	var directors []model.Person
	doc.Find("a[rel='v:directedBy']").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		personID := ""
		if match := regexp.MustCompile(`/celebrity/(\d+)/`).FindStringSubmatch(href); len(match) > 1 {
			personID = match[1]
		}
		directors = append(directors, model.Person{
			ID:   personID,
			Name: strings.TrimSpace(s.Text()),
		})
	})
	directorsJSON, _ := json.Marshal(directors)
	movie.Directors = string(directorsJSON)

	// 主演
	var actors []model.Person
	doc.Find("a[rel='v:starring']").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		personID := ""
		if match := regexp.MustCompile(`/celebrity/(\d+)/`).FindStringSubmatch(href); len(match) > 1 {
			personID = match[1]
		}
		actors = append(actors, model.Person{
			ID:   personID,
			Name: strings.TrimSpace(s.Text()),
		})
	})
	actorsJSON, _ := json.Marshal(actors)
	movie.Actors = string(actorsJSON)

	// 剧情简介
	summary := doc.Find("span[property='v:summary']").Text()
	movie.Summary = strings.TrimSpace(summary)

	// IMDb ID
	if match := regexp.MustCompile(`IMDb:\s*(tt\d+)`).FindStringSubmatch(infoText); len(match) > 1 {
		movie.IMDbID = match[1]
	}

	// 尝试从 JSON-LD 获取更多信息 (JSON-LD 通常最稳定)
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		var ldString = strings.TrimSpace(s.Text())
		// 处理由于 HTML 转移导致的非法 JSON
		ldString = strings.ReplaceAll(ldString, "\n", "")

		var ldMap map[string]interface{}
		if err := json.Unmarshal([]byte(ldString), &ldMap); err == nil {
			// 如果有 name，通常是电影名
			if name, ok := ldMap["name"].(string); ok && name != "" {
				// 优先使用 JSON-LD 的标题，因为它不受 span 嵌套影响
				movie.Title = strings.TrimSpace(name)
			}
			// 描述
			if desc, ok := ldMap["description"].(string); ok && movie.Summary == "" {
				movie.Summary = strings.TrimSpace(desc)
			}
			// 海报
			if image, ok := ldMap["image"].(string); ok && movie.Poster == "" {
				movie.Poster = image
			}
		}
	})

	return movie
}

// EnrichMovieWithVector 为电影生成向量并存储原始内容
func (c *DoubanCrawler) EnrichMovieWithVector(movie *model.Movie) error {
	// 按照约定模板拼接原始内容
	// 标题: {Title} | 类型: {Genres} | 导演: {Directors} | 主演: {Actors} | 剧情简介: {Summary}

	// 解析导演和演员名称
	var directors []model.Person
	var actors []model.Person
	json.Unmarshal([]byte(movie.Directors), &directors)
	json.Unmarshal([]byte(movie.Actors), &actors)

	var dirNames []string
	for _, d := range directors {
		dirNames = append(dirNames, d.Name)
	}
	var actNames []string
	for i, a := range actors {
		if i >= 5 {
			break
		} // 只取前5个演员，避免文本过长
		actNames = append(actNames, a.Name)
	}

	rawContent := fmt.Sprintf("标题: %s | 类型: %s | 导演: %s | 主演: %s | 剧情简介: %s",
		movie.Title,
		movie.Genres,
		strings.Join(dirNames, ","),
		strings.Join(actNames, ","),
		movie.Summary,
	)

	// 截断过长文本（保留前1000个字符）
	if len([]rune(rawContent)) > 1000 {
		rawContent = string([]rune(rawContent)[:1000])
	}

	movie.EmbeddingContent = rawContent

	// 调用 Ollama 生成向量
	vec, err := utils.GenerateEmbedding(rawContent)
	if err != nil {
		return err
	}

	// 验证向量维度 (chentinz/bge-base-zh-v1.5 应该是 768 维)
	if len(vec) == 0 {
		return fmt.Errorf("Ollama 返回了空向量")
	}

	if len(vec) != 768 {
		return fmt.Errorf("向量维度不匹配: 期望 768, 实际 %d", len(vec))
	}

	// 存储为 pgvector.Vector 指针
	v := pgvector.NewVector(vec)
	movie.Embedding = &v

	return nil
}

// CrawlAsync 异步爬取电影信息
func (c *DoubanCrawler) CrawlAsync(doubanID string) {
	go func() {
		if err := c.CrawlDoubanMovie(doubanID); err != nil {
			log.Printf("[爬虫] 爬取失败 (豆瓣ID: %s): %v", doubanID, err)
		}
	}()
}

// SearchSuggest 电影搜索建议
func (c *DoubanCrawler) SearchSuggest(keyword string) ([]MovieSuggestResponse, error) {
	// 检查缓存
	cacheKey := fmt.Sprintf("douban_suggest:%s", keyword)
	if cached, found := utils.CacheGet(cacheKey); found {
		if results, ok := cached.([]MovieSuggestResponse); ok {
			return results, nil
		}
	}

	// 调用豆瓣API
	url := fmt.Sprintf("https://movie.douban.com/j/subject_suggest?q=%s", keyword)

	// 使用自定义HTTP客户端
	client := utils.NewHTTPClient()
	var doubanResults []DoubanMovieSuggest

	if err := client.GetJSON(url, &doubanResults); err != nil {
		return nil, fmt.Errorf("豆瓣API调用失败: %w", err)
	}

	// 转换数据格式
	var results []MovieSuggestResponse
	for _, item := range doubanResults {
		// 使用本地图片代理，绕过防盗链
		proxyImg := fmt.Sprintf("/api/proxy/image?url=%s", item.Img)

		results = append(results, MovieSuggestResponse{
			ID:       item.ID,
			Title:    item.Title,
			SubTitle: item.SubTitle,
			Type:     item.Type,
			Year:     item.Year,
			Episode:  item.Episode,
			Img:      proxyImg,
		})
	}

	// 缓存结果，缓存时间5分钟
	utils.CacheSet(cacheKey, results, 5*time.Minute)

	return results, nil
}

// GetPopularSubjects 获取热门电影/电视剧
func (c *DoubanCrawler) GetPopularSubjects(movieType string) ([]DoubanPopularSubject, error) {
	// 检查缓存
	cacheKey := fmt.Sprintf("douban_popular:%s", movieType)
	if cached, found := utils.CacheGet(cacheKey); found {
		if results, ok := cached.([]DoubanPopularSubject); ok {
			return results, nil
		}
	}

	var url string
	switch movieType {
	case "movie":
		url = "https://movie.douban.com/j/search_subjects?type=movie&tag=热门&page_limit=50&page_start=0"
	case "tv":
		url = "https://movie.douban.com/j/search_subjects?type=tv&tag=热门&page_limit=50&page_start=0"
	case "show":
		url = "https://movie.douban.com/j/search_subjects?type=tv&tag=综艺&page_limit=50&page_start=0"
	case "cartoon":
		url = "https://movie.douban.com/j/search_subjects?type=tv&tag=日本动画&page_limit=50&page_start=0"
	default:
		return nil, fmt.Errorf("不支持的电影类型: %s", movieType)
	}

	// 使用自定义HTTP客户端
	client := utils.NewHTTPClient()
	var response DoubanPopularResponse

	if err := client.GetJSON(url, &response); err != nil {
		return nil, fmt.Errorf("豆瓣热门数据抓取失败: %w", err)
	}

	// 处理图片，使用代理绕过防盗链
	for i := range response.Subjects {
		response.Subjects[i].Cover = fmt.Sprintf("/api/proxy/image?url=%s", response.Subjects[i].Cover)
	}

	// 缓存结果，缓存时间1小时
	utils.CacheSet(cacheKey, response.Subjects, 1*time.Hour)

	return response.Subjects, nil
}
