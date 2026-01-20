package service

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/pgvector/pgvector-go"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
	"golang.org/x/sync/singleflight"
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
	sf        singleflight.Group // 防止并发重复抓取同一电影
}

// NewDoubanCrawler 创建爬虫服务
func NewDoubanCrawler(movieRepo *repository.MovieRepository) *DoubanCrawler {
	return &DoubanCrawler{
		movieRepo: movieRepo,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		sf: singleflight.Group{},
	}
}

// generateBid 随机生成 11 位 bid (模拟豆瓣用户ID Cookie)
func (c *DoubanCrawler) generateBid() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	bid := make([]byte, 11)
	for i := range bid {
		bid[i] = chars[rand.Intn(len(chars))]
	}
	return string(bid)
}
func isValidDoubanID(id string) bool {
	// 长度校验：6～9 位
	if len(id) < 6 || len(id) > 9 {
		return false
	}
	// 纯数字校验
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// CrawlDoubanMovie 爬取豆瓣电影详情页
func (c *DoubanCrawler) CrawlDoubanMovie(doubanID string) error {
	if !isValidDoubanID(doubanID) {
		return fmt.Errorf("无效的豆瓣ID:%s", doubanID)
	}
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
	req.Header.Set("Cookie", fmt.Sprintf(`ll="108288"; bid=%s; _pk_id.100001.4cf6=8bbc2ff56b7eadbf.1768570153.; _pk_ses.100001.4cf6=1; ap_v=0,6.0; __yadk_uid=Q9rDtuhs4wWFa5rwTvB6WpPxXFFRnScS; __utma=30149280.1349976959.1768570154.1768570154.1768570154.1; __utmb=30149280.0.10.1768570154; __utmc=30149280; __utmz=30149280.1768570154.1.1.utmcsr=(direct)|utmccn=(direct)|utmcmd=(none); __utma=223695111.369068564.1768570154.1768570154.1768570154.1; __utmb=223695111.0.10.1768570154; __utmc=223695111; __utmz=223695111.1768570154.1.1.utmcsr=(direct)|utmccn=(direct)|utmcmd=(none); _vwo_uuid_v2=D25CE55BF48CA21000B5BED858D7F40A3|a607d8885a8913b757f280679b32e9b1`, c.generateBid()))

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

// CrawlMovieSafe 安全抓取电影信息(防止并发重复抓取)
// 使用 singleflight 确保同一 doubanID 在同一时间只会被抓取一次
func (c *DoubanCrawler) CrawlMovieSafe(doubanID string) error {
	// 先快速检查数据库，避免不必要的 singleflight 等待
	movie, _ := c.movieRepo.FindByDoubanID(doubanID)
	if movie != nil {
		log.Printf("[爬虫] 电影已存在数据库，跳过抓取: %s", doubanID)
		return nil
	}

	// 使用 singleflight 防止并发抓取同一个 doubanID
	// 相同的 doubanID 只会执行一次抓取，其他并发请求会等待并共享结果
	_, err, _ := c.sf.Do(doubanID, func() (interface{}, error) {
		// 在 singleflight 内部再次检查，防止等待期间已被其他 goroutine 完成
		if m, _ := c.movieRepo.FindByDoubanID(doubanID); m != nil {
			log.Printf("[爬虫] 电影在等待期间已被抓取: %s", doubanID)
			return m, nil
		}

		// 执行实际抓取
		log.Printf("[爬虫] 开始安全抓取电影: %s", doubanID)
		return nil, c.CrawlDoubanMovie(doubanID)
	})

	return err
}

// CrawlMovieSafeAsync 异步安全抓取电影信息
// 适用于播放页等场景，不需要等待抓取结果
func (c *DoubanCrawler) CrawlMovieSafeAsync(doubanID string) {
	go func() {
		if err := c.CrawlMovieSafe(doubanID); err != nil {
			log.Printf("[爬虫] 异步安全抓取失败 (豆瓣ID: %s): %v", doubanID, err)
		}
	}()
}

// SearchSuggest 电影搜索建议（优先从数据库搜索，无结果时调用豆瓣API）
func (c *DoubanCrawler) SearchSuggest(keyword string) ([]MovieSuggestResponse, error) {
	// 如果关键词为空，直接返回
	if strings.TrimSpace(keyword) == "" {
		return []MovieSuggestResponse{}, nil
	}

	// 检查缓存
	cacheKey := fmt.Sprintf("movie_suggest:%s", keyword)
	if cached, found := utils.CacheGet(cacheKey); found {
		if results, ok := cached.([]MovieSuggestResponse); ok {
			return results, nil
		}
	}

	// 1. 先从数据库模糊搜索
	dbResults, err := c.searchFromDB(keyword)
	if err != nil {
		log.Printf("[搜索建议] 数据库搜索失败: %v，降级到豆瓣API", err)
	}

	// 如果数据库有结果，直接返回
	if len(dbResults) > 0 {
		log.Printf("[搜索建议] 从数据库返回 %d 条结果 (关键词: %s)", len(dbResults), keyword)
		// 缓存结果，缓存时间10分钟
		utils.CacheSet(cacheKey, dbResults, 10*time.Minute)
		return dbResults, nil
	}

	// 2. 数据库无结果，调用豆瓣API
	log.Printf("[搜索建议] 数据库无结果，调用豆瓣API (关键词: %s)", keyword)
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
		proxyImg := fmt.Sprintf("https://image.baidu.com/search/down?url=%s", item.Img)

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

// searchFromDB 从数据库模糊搜索电影
func (c *DoubanCrawler) searchFromDB(keyword string) ([]MovieSuggestResponse, error) {
	var movies []model.Movie

	// 模糊搜索：title 或 original_title 包含关键词
	// 按评分和更新时间排序，限制返回10条
	err := c.movieRepo.DB.Where("title LIKE ? OR original_title LIKE ?",
		"%"+keyword+"%", "%"+keyword+"%").
		Order("rating DESC, updated_at DESC").
		Limit(10).
		Find(&movies).Error

	if err != nil {
		return nil, fmt.Errorf("数据库查询失败: %w", err)
	}

	// 转换为 MovieSuggestResponse
	var results []MovieSuggestResponse
	for _, movie := range movies {
		// 推断类型
		movieType := c.inferMovieType(movie.Genres)

		results = append(results, MovieSuggestResponse{
			ID:       movie.DoubanID,
			Title:    movie.Title,
			SubTitle: movie.OriginalTitle,
			Type:     movieType,
			Year:     movie.Year,
			Episode:  "", // 数据库没有集数信息
			Img:      movie.Poster,
		})
	}

	return results, nil
}

// inferMovieType 根据类型推断电影类别
func (c *DoubanCrawler) inferMovieType(genres string) string {
	genresLower := strings.ToLower(genres)

	if strings.Contains(genresLower, "电视剧") {
		return "tv"
	}
	if strings.Contains(genresLower, "综艺") {
		return "show"
	}
	if strings.Contains(genresLower, "动画") || strings.Contains(genresLower, "动漫") {
		return "cartoon"
	}
	return "movie"
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

	// 正常抓取逻辑
	if err := client.GetJSON(url, &response); err != nil {
		log.Printf("[爬虫] 豆瓣热门数据抓取失败: %v, 尝试读取备选缓存", err)
		// 抓取失败，尝试从备选缓存读取
		fallbackKey := fmt.Sprintf("fallback:%s", cacheKey)
		if cached, found := utils.CacheGet(fallbackKey); found {
			if results, ok := cached.([]DoubanPopularSubject); ok {
				log.Printf("[爬虫] 成功降级使用备选缓存数据 (%s)", movieType)
				return results, nil
			}
		}
		return nil, fmt.Errorf("豆瓣热门数据抓取失败且无备选缓存: %w", err)
	}

	// 处理图片，使用代理绕过防盗链
	for i := range response.Subjects {
		response.Subjects[i].Cover = fmt.Sprintf("https://image.baidu.com/search/down?url=%s", response.Subjects[i].Cover)
	}

	// 缓存结果，缓存时间12小时
	utils.CacheSet(cacheKey, response.Subjects, 12*time.Hour)
	// 同时更新备选缓存（永不过期），用于抓取失败时降级
	utils.CacheSet(fmt.Sprintf("fallback:%s", cacheKey), response.Subjects, 0)

	return response.Subjects, nil
}

// DoubanReview 豆瓣短评
type DoubanReview struct {
	Title     string `json:"title"`     // 评论标题
	Author    string `json:"author"`    // 作者
	Link      string `json:"link"`      // 链接
	Published string `json:"published"` // 发布时间
	Summary   string `json:"summary"`   // 内容摘要
}

// rssFeed RSS 2.0 feed 结构
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

// rssChannel RSS channel 结构
type rssChannel struct {
	Items []rssItem `xml:"item"`
}

// rssItem RSS item 结构
type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Creator     string `xml:"http://purl.org/dc/elements/1.1/ creator"` // dc:creator
	PubDate     string `xml:"pubDate"`
}

// GetReviews 获取豆瓣短评
func (c *DoubanCrawler) GetReviews(doubanID string) ([]DoubanReview, error) {
	if !isValidDoubanID(doubanID) {
		return nil, fmt.Errorf("无效的豆瓣ID:%s", doubanID)
	}
	// 检查缓存
	cacheKey := fmt.Sprintf("douban_reviews:%s", doubanID)
	if cached, found := utils.CacheGet(cacheKey); found {
		if reviews, ok := cached.([]DoubanReview); ok {
			return reviews, nil
		}
	}

	url := fmt.Sprintf("https://www.douban.com/feed/subject/%s/reviews", doubanID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头，模拟浏览器
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/xml,text/xml,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://movie.douban.com/")
	req.Header.Set("Cookie", fmt.Sprintf(`ll="108288"; bid=%s`, c.generateBid()))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求返回状态码: %d", resp.StatusCode)
	}

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 调试日志：输出响应体前 500 字符
	debugBody := string(body)
	if len(debugBody) > 500 {
		debugBody = debugBody[:500]
	}
	// log.Printf("[爬虫] 豆瓣短评 RSS 响应 (豆瓣ID: %s): %s...", doubanID, debugBody)

	// 解析 RSS 2.0 XML
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("解析 XML 失败: %w", err)
	}

	log.Printf("[爬虫] 解析到 %d 条 item (豆瓣ID: %s)", len(feed.Channel.Items), doubanID)

	// 转换为 DoubanReview
	var reviews []DoubanReview
	for _, item := range feed.Channel.Items {
		// 解析时间，RSS 使用 RFC1123 格式 (例如: Fri, 21 Nov 2025 01:23:16 GMT)
		published := item.PubDate
		if t, err := time.Parse(time.RFC1123, item.PubDate); err == nil {
			published = t.Format("2006-01-02")
		}

		// 从 description 中提取摘要（去掉 CDATA 包裹的 JSON 部分，取纯文本）
		summary := extractReviewSummary(item.Description)

		reviews = append(reviews, DoubanReview{
			Title:     strings.TrimSpace(item.Title),
			Author:    strings.TrimSpace(item.Creator),
			Link:      strings.TrimSpace(item.Link),
			Published: published,
			Summary:   summary,
		})
	}

	// 缓存结果，缓存时间 24 小时
	utils.CacheSet(cacheKey, reviews, 24*time.Hour)

	// 持久化到数据库
	if reviewsJSON, err := json.Marshal(reviews); err == nil {
		if err := c.movieRepo.UpdateReviews(doubanID, string(reviewsJSON)); err != nil {
			log.Printf("[爬虫] 持久化短评失败 (豆瓣ID: %s): %v", doubanID, err)
		}
	}

	log.Printf("[爬虫] 成功获取并在库中更新豆瓣短评 (豆瓣ID: %s), 共 %d 条", doubanID, len(reviews))
	return reviews, nil
}

// extractReviewSummary 从 RSS description 中提取评论摘要
func extractReviewSummary(desc string) string {
	// description 格式通常是:
	// "用户名评论: 影片名 (链接)\n评价: XXX\n\n{JSON内容或纯文本...}"
	// 我们需要提取评价后面的内容

	lines := strings.Split(desc, "\n")
	var result []string
	skipPrefix := true

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 跳过开头的 "用户评论:" 和 "评价:" 行
		if skipPrefix {
			if strings.Contains(line, "评论:") || strings.Contains(line, "评价:") {
				continue
			}
			skipPrefix = false
		}

		// 如果遇到 JSON 格式的内容，跳过
		if strings.HasPrefix(line, "{") {
			continue
		}

		result = append(result, line)
	}

	summary := strings.Join(result, " ")

	// 限制长度
	if len([]rune(summary)) > 300 {
		summary = string([]rune(summary)[:300]) + "..."
	}

	return summary
}
