package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultProductionSecret = "replace-with-a-long-random-secret"
const minimumProductionSecretBytes = 32

// Config 保存重构应用运行所需的进程级配置，各功能模块的配置也会在加载时统一校验。
type Config struct {
	Env                     string
	Port                    string
	SiteName                string
	SiteURL                 string
	WebRoot                 string
	ShutdownTimeout         time.Duration
	JWTExpiry               time.Duration
	HTTP                    HTTPConfig
	Search                  SearchConfig
	Popularity              PopularityConfig
	Catalog                 CatalogConfig
	Danmaku                 DanmakuConfig
	Database                DatabaseConfig
	OutboundMaxConnsPerHost int
	AppSecret               string
	JobsInWeb               bool
	Worker                  WorkerConfig
}

type WorkerConfig struct {
	Concurrency int
	Poll        time.Duration
}

// HTTPConfig 保存单实例请求、连接、请求体和访问日志预算。
type HTTPConfig struct {
	MaxInFlight            int
	MaxHeavyInFlight       int
	MaxImageInFlight       int
	QueueTimeout           time.Duration
	RequestTimeout         time.Duration
	MaxBodyBytes           int64
	MaxHeaderBytes         int
	MaxConnections         int
	AccessLogSamplePercent int
	AccessLogMaxPerSecond  int
}

// CatalogConfig 保存影视资料、向量和可选 AI Gateway 的外部服务配置。
type CatalogConfig struct {
	TMDBToken    string
	OllamaHost   string
	OllamaModel  string
	CFGatewayURL string
	CFAPIToken   string
	CFAIModel    string
	// AITimeout 单独给 AI Gateway 用。一次非流式 chat completion 动辄几十秒，
	// 沿用搜索源的秒级超时会让语义改写必然失败。
	AITimeout time.Duration
	// IMDbLookupInterval 是 wmdb 豆瓣→IMDb 映射查询的最小发送间隔。
	// wmdb 只作为批量回填的兜底，这里配的是它的节流节奏。
	IMDbLookupInterval time.Duration
	// WikidataEndpoint 和 WikidataUserAgent 用于批量补齐豆瓣→IMDb 映射。
	// 维基媒体要求请求带上能说明来源和联系方式的 User-Agent，默认 UA 会被拒绝。
	WikidataEndpoint  string
	WikidataUserAgent string
	// IMDbBackfillBatch 是单轮批量查询的对象数量。SPARQL 查询有 60 秒超时，
	// 批太大会整批失败，200 是经验上比较稳的规模。
	IMDbBackfillBatch int
	// WikidataTimeout 单独给 SPARQL 用：批量查询比普通抓取慢得多。
	WikidataTimeout time.Duration
}

// DanmakuConfig 保存可选弹幕服务地址；为空时弹幕能力安全降级。
type DanmakuConfig struct {
	APIBase string
}

// DatabaseConfig 保存隔离新库连接信息和连接池上限。
type DatabaseConfig struct {
	Enabled  bool
	Migrate  bool
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
	TimeZone string
	MaxConns int
}

// SearchConfig 保存上游搜索预算、缓存、熔断和媒体匹配灰度参数。
type SearchConfig struct {
	SourceTimeout             time.Duration
	TotalTimeout              time.Duration
	CacheTTL                  time.Duration
	CacheEntries              int
	SourceMaxConcurrency      int
	BackgroundMaxConcurrency  int
	BreakerEnabled            bool
	ResourceMatchShadow       bool
	ResourceMatchAutoApply    bool
	MediaAutoMatchThreshold   float64
	MediaReviewMatchThreshold float64
}

// PopularityConfig 控制热门快照的刷新周期。快照构建与读取已固定启用。
type PopularityConfig struct {
	RefreshInterval time.Duration
}

// Load 从环境变量构建完整配置并执行跨字段安全校验。
// 返回成功后，Web/Worker 才可以创建数据库连接或开始监听端口。
func Load() (Config, error) {
	appEnv := env("APP_ENV", "development")
	shutdownSeconds, err := positiveIntEnv("SHUTDOWN_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	sourceTimeoutSeconds, err := positiveIntEnv("SEARCH_SOURCE_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	totalTimeoutSeconds, err := positiveIntEnv("SEARCH_TOTAL_TIMEOUT_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	cacheMinutes, err := positiveIntEnv("SEARCH_CACHE_MINUTES", 180)
	if err != nil {
		return Config{}, err
	}
	cacheEntries, err := positiveIntEnv("SEARCH_CACHE_ENTRIES", 200)
	if err != nil {
		return Config{}, err
	}
	sourceMaxConcurrency, err := positiveIntEnv("SEARCH_SOURCE_MAX_CONCURRENCY", 6)
	if err != nil {
		return Config{}, err
	}
	backgroundMaxConcurrency, err := positiveIntEnv("SEARCH_BACKGROUND_MAX_CONCURRENCY", 8)
	if err != nil {
		return Config{}, err
	}
	httpMaxInFlight, err := positiveIntEnv("HTTP_MAX_IN_FLIGHT", 64)
	if err != nil {
		return Config{}, err
	}
	httpMaxHeavyInFlight, err := positiveIntEnv("HTTP_MAX_HEAVY_IN_FLIGHT", 12)
	if err != nil {
		return Config{}, err
	}
	httpMaxImageInFlight, err := positiveIntEnv("HTTP_MAX_IMAGE_IN_FLIGHT", 24)
	if err != nil {
		return Config{}, err
	}
	httpQueueTimeoutMilliseconds, err := positiveIntEnv("HTTP_QUEUE_TIMEOUT_MILLISECONDS", 100)
	if err != nil {
		return Config{}, err
	}
	httpRequestTimeoutSeconds, err := positiveIntEnv("HTTP_REQUEST_TIMEOUT_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	httpMaxBodyBytes, err := positiveIntEnv("HTTP_MAX_BODY_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	httpMaxHeaderBytes, err := positiveIntEnv("HTTP_MAX_HEADER_BYTES", 64<<10)
	if err != nil {
		return Config{}, err
	}
	httpMaxConnections, err := positiveIntEnv("HTTP_MAX_CONNECTIONS", 512)
	if err != nil {
		return Config{}, err
	}
	accessLogDefault := 100
	if appEnv == "production" {
		accessLogDefault = 10
	}
	httpAccessLogSamplePercent, err := nonNegativeIntEnv("HTTP_ACCESS_LOG_SAMPLE_PERCENT", accessLogDefault)
	if err != nil || httpAccessLogSamplePercent > 100 {
		return Config{}, errors.New("HTTP_ACCESS_LOG_SAMPLE_PERCENT must be between 0 and 100")
	}
	accessLogRateDefault := 100
	if appEnv == "production" {
		accessLogRateDefault = 20
	}
	httpAccessLogMaxPerSecond, err := positiveIntEnv("HTTP_ACCESS_LOG_MAX_PER_SECOND", accessLogRateDefault)
	if err != nil {
		return Config{}, err
	}
	outboundMaxConnsPerHost, err := positiveIntEnv("OUTBOUND_MAX_CONNS_PER_HOST", 12)
	if err != nil {
		return Config{}, err
	}
	databaseMaxConns, err := positiveIntEnv("DB_MAX_CONNS", 12)
	if err != nil {
		return Config{}, err
	}
	mediaAutoMatchThreshold, err := probabilityEnv("MEDIA_AUTO_MATCH_THRESHOLD", 0.88)
	if err != nil {
		return Config{}, err
	}
	mediaReviewMatchThreshold, err := probabilityEnv("MEDIA_REVIEW_MATCH_THRESHOLD", 0.68)
	if err != nil {
		return Config{}, err
	}
	jwtExpiryHours, err := positiveIntEnv("JWT_EXPIRY_HOURS", 72)
	if err != nil {
		return Config{}, err
	}
	workerPollSeconds, err := positiveIntEnv("WORKER_POLL_SECONDS", 2)
	if err != nil {
		return Config{}, err
	}
	workerConcurrency, err := positiveIntEnv("WORKER_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}
	popularityRefreshMinutes, err := positiveIntEnv("POPULARITY_REFRESH_MINUTES", 30)
	if err != nil {
		return Config{}, err
	}
	catalogAITimeoutSeconds, err := positiveIntEnv("CATALOG_AI_TIMEOUT_SECONDS", 90)
	if err != nil {
		return Config{}, err
	}
	imdbLookupIntervalMilliseconds, err := positiveIntEnv("IMDB_LOOKUP_INTERVAL_MS", 1200)
	if err != nil {
		return Config{}, err
	}
	imdbBackfillBatch, err := positiveIntEnv("IMDB_BACKFILL_BATCH", 200)
	if err != nil {
		return Config{}, err
	}
	wikidataTimeoutSeconds, err := positiveIntEnv("WIKIDATA_TIMEOUT_SECONDS", 60)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Env:             appEnv,
		Port:            env("PORT", "5008"),
		SiteName:        env("SITE_NAME", "Moovie影牛"),
		SiteURL:         strings.TrimRight(env("SITE_URL", "http://localhost:5008"), "/"),
		WebRoot:         env("WEB_ROOT", "./web"),
		ShutdownTimeout: time.Duration(shutdownSeconds) * time.Second,
		JWTExpiry:       time.Duration(jwtExpiryHours) * time.Hour,
		HTTP: HTTPConfig{
			MaxInFlight:            httpMaxInFlight,
			MaxHeavyInFlight:       httpMaxHeavyInFlight,
			MaxImageInFlight:       httpMaxImageInFlight,
			QueueTimeout:           time.Duration(httpQueueTimeoutMilliseconds) * time.Millisecond,
			RequestTimeout:         time.Duration(httpRequestTimeoutSeconds) * time.Second,
			MaxBodyBytes:           int64(httpMaxBodyBytes),
			MaxHeaderBytes:         httpMaxHeaderBytes,
			MaxConnections:         httpMaxConnections,
			AccessLogSamplePercent: httpAccessLogSamplePercent,
			AccessLogMaxPerSecond:  httpAccessLogMaxPerSecond,
		},
		OutboundMaxConnsPerHost: outboundMaxConnsPerHost,
		AppSecret:               env("APP_SECRET", defaultProductionSecret),
		JobsInWeb:               env("JOBS_IN_WEB", "true") == "true",
		Worker: WorkerConfig{
			Concurrency: workerConcurrency,
			Poll:        time.Duration(workerPollSeconds) * time.Second,
		},
		Search: SearchConfig{
			SourceTimeout:             time.Duration(sourceTimeoutSeconds) * time.Second,
			TotalTimeout:              time.Duration(totalTimeoutSeconds) * time.Second,
			CacheTTL:                  time.Duration(cacheMinutes) * time.Minute,
			CacheEntries:              cacheEntries,
			SourceMaxConcurrency:      sourceMaxConcurrency,
			BackgroundMaxConcurrency:  backgroundMaxConcurrency,
			BreakerEnabled:            env("SEARCH_BREAKER_ENABLED", "true") != "false",
			ResourceMatchShadow:       env("RESOURCE_MATCH_SHADOW", "true") != "false",
			ResourceMatchAutoApply:    env("RESOURCE_MATCH_AUTO_APPLY", "false") == "true",
			MediaAutoMatchThreshold:   mediaAutoMatchThreshold,
			MediaReviewMatchThreshold: mediaReviewMatchThreshold,
		},
		Popularity: PopularityConfig{
			RefreshInterval: time.Duration(popularityRefreshMinutes) * time.Minute,
		},
		Catalog: CatalogConfig{
			TMDBToken:    env("TMDB_API_TOKEN", ""),
			OllamaHost:   strings.TrimRight(env("OLLAMA_HOST", "http://localhost:11434"), "/"),
			OllamaModel:  env("OLLAMA_MODEL", "quentinz/bge-base-zh-v1.5"),
			CFGatewayURL: strings.TrimRight(env("CF_GATEWAY_URL", ""), "/"),
			CFAPIToken:   env("CF_API_TOKEN", ""),
			CFAIModel:    env("CF_AI_MODEL", "custom-alibaba-coding/kimi-k2.5"),

			AITimeout:          time.Duration(catalogAITimeoutSeconds) * time.Second,
			IMDbLookupInterval: time.Duration(imdbLookupIntervalMilliseconds) * time.Millisecond,
			WikidataEndpoint:   strings.TrimRight(env("WIKIDATA_SPARQL_URL", ""), "/"),
			WikidataUserAgent:  env("WIKIDATA_USER_AGENT", ""),
			IMDbBackfillBatch:  imdbBackfillBatch,
			WikidataTimeout:    time.Duration(wikidataTimeoutSeconds) * time.Second,
		},
		Danmaku: DanmakuConfig{APIBase: strings.TrimRight(env("DANMU_API_BASE", ""), "/")},
		Database: DatabaseConfig{
			Enabled:  env("DB_ENABLED", "false") == "true",
			Migrate:  env("DB_AUTO_MIGRATE", "true") == "true",
			Host:     env("DB_HOST", "localhost"),
			Port:     env("DB_PORT", "5432"),
			User:     env("DB_USER", "postgres"),
			Password: env("DB_PASSWORD", "postgres"),
			Name:     env("DB_NAME", "moovie_new"),
			SSLMode:  env("DB_SSLMODE", "disable"),
			TimeZone: env("DB_TIMEZONE", "Asia/Shanghai"),
			MaxConns: databaseMaxConns,
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Port == "" {
		return errors.New("PORT must not be empty")
	}
	if c.SiteName == "" || c.SiteURL == "" || c.WebRoot == "" {
		return errors.New("SITE_NAME, SITE_URL and WEB_ROOT must not be empty")
	}
	parsedSiteURL, err := url.Parse(c.SiteURL)
	if err != nil || parsedSiteURL.Host == "" || (parsedSiteURL.Scheme != "http" && parsedSiteURL.Scheme != "https") {
		return errors.New("SITE_URL must be an absolute http or https URL")
	}
	if c.Database.Name == "" {
		return errors.New("DB_NAME must not be empty")
	}
	if c.HTTP.MaxInFlight > 1024 || c.HTTP.MaxHeavyInFlight > c.HTTP.MaxInFlight || c.HTTP.MaxImageInFlight > c.HTTP.MaxInFlight {
		return errors.New("HTTP concurrency limits must be positive, at most 1024, and class limits must not exceed HTTP_MAX_IN_FLIGHT")
	}
	if c.HTTP.MaxConnections > 8192 || (c.HTTP.MaxConnections > 0 && c.HTTP.MaxInFlight > 0 && c.HTTP.MaxConnections < c.HTTP.MaxInFlight) {
		return errors.New("HTTP_MAX_CONNECTIONS must be between HTTP_MAX_IN_FLIGHT and 8192")
	}
	if c.HTTP.MaxBodyBytes > 16<<20 || c.HTTP.MaxHeaderBytes > 1<<20 {
		return errors.New("HTTP request body/header limits exceed the reviewed safety boundary")
	}
	if c.HTTP.AccessLogMaxPerSecond > 1000 {
		return errors.New("HTTP_ACCESS_LOG_MAX_PER_SECOND must not exceed 1000")
	}
	if c.Search.SourceMaxConcurrency > 64 || c.Search.BackgroundMaxConcurrency > 64 {
		return errors.New("search concurrency limits must not exceed 64")
	}
	if c.OutboundMaxConnsPerHost > 128 {
		return errors.New("OUTBOUND_MAX_CONNS_PER_HOST must not exceed 128")
	}
	if c.Catalog.AITimeout > 10*time.Minute {
		return errors.New("CATALOG_AI_TIMEOUT_SECONDS must not exceed 600")
	}
	if c.Catalog.IMDbLookupInterval > time.Minute {
		return errors.New("IMDB_LOOKUP_INTERVAL_MS must not exceed 60000")
	}
	if c.Catalog.IMDbBackfillBatch > 500 {
		return errors.New("IMDB_BACKFILL_BATCH must not exceed 500")
	}
	if c.Catalog.WikidataTimeout > 5*time.Minute {
		return errors.New("WIKIDATA_TIMEOUT_SECONDS must not exceed 300")
	}
	if c.Database.MaxConns > 100 {
		return errors.New("DB_MAX_CONNS must not exceed 100")
	}
	if c.Worker.Concurrency > 64 {
		return errors.New("WORKER_CONCURRENCY must not exceed 64")
	}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return fmt.Errorf("unsupported APP_ENV %q", c.Env)
	}
	if strings.EqualFold(c.Database.Name, "moovie") {
		return errors.New("refusing to use legacy database name moovie during isolated refactor")
	}
	if !c.Database.Enabled && !c.JobsInWeb {
		return errors.New("JOBS_IN_WEB=false requires DB_ENABLED=true and a separate worker")
	}
	if c.Search.MediaAutoMatchThreshold > 0 || c.Search.MediaReviewMatchThreshold > 0 {
		if c.Search.MediaAutoMatchThreshold <= 0 || c.Search.MediaAutoMatchThreshold > 1 ||
			c.Search.MediaReviewMatchThreshold <= 0 || c.Search.MediaReviewMatchThreshold >= c.Search.MediaAutoMatchThreshold {
			return errors.New("MEDIA_REVIEW_MATCH_THRESHOLD must be greater than 0 and lower than MEDIA_AUTO_MATCH_THRESHOLD")
		}
	}
	if c.Env == "production" {
		if !c.Database.Enabled {
			return errors.New("DB_ENABLED=true is required in production")
		}
		if parsedSiteURL.Scheme != "https" {
			return errors.New("SITE_URL must use https in production")
		}
		if c.AppSecret == defaultProductionSecret || len([]byte(c.AppSecret)) < minimumProductionSecretBytes {
			return fmt.Errorf("APP_SECRET must contain at least %d bytes and must be replaced in production", minimumProductionSecretBytes)
		}
	}
	return nil
}

func (c DatabaseConfig) DSN() string {
	query := url.Values{}
	query.Set("sslmode", c.SSLMode)
	query.Set("TimeZone", c.TimeZone)
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     net.JoinHostPort(c.Host, c.Port),
		Path:     "/" + c.Name,
		RawQuery: query.Encode(),
	}).String()
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func positiveIntEnv(key string, fallback int) (int, error) {
	raw := env(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func nonNegativeIntEnv(key string, fallback int) (int, error) {
	raw := env(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}

func probabilityEnv(key string, fallback float64) (float64, error) {
	raw := env(key, strconv.FormatFloat(fallback, 'f', -1, 64))
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 || value > 1 {
		return 0, fmt.Errorf("%s must be greater than 0 and at most 1", key)
	}
	return value, nil
}
