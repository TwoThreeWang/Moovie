package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config 应用配置
type Config struct {
	Env         string
	AppSecret   string
	DatabaseURL string
	JWTExpiry   time.Duration
	Port        string
	SiteName    string
	SiteUrl     string
	TimeZone    string
	TMDBToken   string
	GeminiKey   string
	GeminiModel string
	CFGatewayURL string
	CFAPIToken   string
	CFAIModel    string

	// SearchBreakerEnabled 采集站点熔断开关。
	// 关闭时仍然统计健康度，只是不再跳过任何站点。出问题改这个变量重启即可恢复原行为。
	SearchBreakerEnabled bool
	// WriteTimeout HTTP 响应写超时。必须显著大于单站点采集超时，
	// 否则冷门词首次搜索（同步等待各采集站）会被 HTTP Server 直接截断。
	WriteTimeout time.Duration
}

// Load 加载配置
func Load() *Config {
	expiryHours, _ := strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "72"))

	dbUser := getEnv("DB_USER", "postgres")
	dbPass := getEnv("DB_PASSWORD", "postgres")
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbName := getEnv("DB_NAME", "moovie")
	dbSSL := getEnv("DB_SSLMODE", "disable")

	dbTZ := getEnv("DB_TIMEZONE", "Asia/Shanghai")

	// PostgreSQL DSN 支持 TimeZone 参数
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&TimeZone=%s",
		dbUser, dbPass, dbHost, dbPort, dbName, dbSSL, dbTZ)

	appSecret := getEnv("APP_SECRET", getEnv("JWT_SECRET", "your-secret-key-change-in-production"))

	writeTimeoutSec, err := strconv.Atoi(getEnv("HTTP_WRITE_TIMEOUT_SECONDS", "30"))
	if err != nil || writeTimeoutSec <= 0 {
		writeTimeoutSec = 30
	}

	if getEnv("APP_ENV", "development") == "production" && appSecret == "your-secret-key-change-in-production" {
		fmt.Println("【严重警告】生产环境正在使用默认密钥！请立即设置 APP_SECRET 环境变量。")
	}

	return &Config{
		Env:         getEnv("APP_ENV", "development"),
		AppSecret:   appSecret,
		DatabaseURL: dbURL,
		JWTExpiry:   time.Duration(expiryHours) * time.Hour,
		Port:        getEnv("PORT", "5007"),
		SiteName:    getEnv("SITE_NAME", "Moovie"),
		SiteUrl:     getEnv("SITE_URL", "http://localhost:5007"),
		TimeZone:    dbTZ,
		TMDBToken:   getEnv("TMDB_API_TOKEN", ""),
		GeminiKey:   getEnv("GEMINI_API_KEY", ""),
		GeminiModel: getEnv("GEMINI_MODEL", "gemini-3-flash-preview"),
		CFGatewayURL: getEnv("CF_GATEWAY_URL", ""),
		CFAPIToken:   getEnv("CF_API_TOKEN", ""),
		CFAIModel:    getEnv("CF_AI_MODEL", "custom-alibaba-coding/kimi-k2.5"),

		SearchBreakerEnabled: getEnv("SEARCH_BREAKER_ENABLED", "true") != "false",
		WriteTimeout:         time.Duration(writeTimeoutSec) * time.Second,
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
