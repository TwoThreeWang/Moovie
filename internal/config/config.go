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

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		dbUser, dbPass, dbHost, dbPort, dbName, dbSSL)

	appSecret := getEnv("APP_SECRET", getEnv("JWT_SECRET", "your-secret-key-change-in-production"))

	return &Config{
		Env:         getEnv("APP_ENV", "development"),
		AppSecret:   appSecret,
		DatabaseURL: dbURL,
		JWTExpiry:   time.Duration(expiryHours) * time.Hour,
		Port:        getEnv("PORT", "8080"),
		SiteName:    getEnv("SITE_NAME", "Moovie"),
		SiteUrl:     getEnv("SITE_URL", "http://localhost:8080"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
