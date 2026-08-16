package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LoadDotEnv 为 Web 和 Worker 入口加载一个轻量、无第三方依赖的 .env 文件。
// 已导出的环境变量优先级更高，因此生产进程管理器无需修改文件即可覆盖本地默认值。
// 容器或 CI 可以不提供该文件。
func LoadDotEnv(path string) error {
	values, err := readDotEnv(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range values {
		if _, exists := os.LookupEnv(entry.key); !exists {
			if err := os.Setenv(entry.key, entry.value); err != nil {
				return fmt.Errorf("set env %s: %w", entry.key, err)
			}
		}
	}
	return nil
}

type dotEnvEntry struct {
	key   string
	value string
}

// readDotEnv 解析 .env 但不修改当前进程环境。只读发布工具借此读取凭据，
// 无需让 shell 执行文件内容；文件不存在时返回 os.ErrNotExist。
func readDotEnv(path string) ([]dotEnvEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer file.Close()

	entries := make([]dotEnvEntry, 0, 16)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			return nil, fmt.Errorf("env file line %d must use KEY=VALUE", lineNumber)
		}
		key := strings.TrimSpace(line[:separator])
		if !validEnvKey(key) {
			return nil, fmt.Errorf("env file line %d has invalid key %q", lineNumber, key)
		}
		value, err := parseDotEnvValue(strings.TrimSpace(line[separator+1:]))
		if err != nil {
			return nil, fmt.Errorf("env file line %d (%s): %w", lineNumber, key, err)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, dotEnvEntry{key: key, value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return entries, nil
}

// DatabaseConfigFromDotEnv 只返回指定 .env 中的数据库配置。
// 这里刻意不允许进程环境覆盖文件，因为调用方需要明确选择待审计的源配置和目标配置。
func DatabaseConfigFromDotEnv(path string) (DatabaseConfig, error) {
	entries, err := readDotEnv(path)
	if err != nil {
		return DatabaseConfig{}, err
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[entry.key] = entry.value
	}
	value := func(key, fallback string) string {
		if configured := strings.TrimSpace(values[key]); configured != "" {
			return configured
		}
		return fallback
	}
	name := strings.TrimSpace(values["DB_NAME"])
	if name == "" {
		return DatabaseConfig{}, errors.New("DB_NAME must be set in the selected env file")
	}
	return DatabaseConfig{
		Enabled:  value("DB_ENABLED", "false") == "true",
		Migrate:  value("DB_AUTO_MIGRATE", "true") == "true",
		Host:     value("DB_HOST", "localhost"),
		Port:     value("DB_PORT", "5432"),
		User:     value("DB_USER", "postgres"),
		Password: value("DB_PASSWORD", "postgres"),
		Name:     name,
		SSLMode:  value("DB_SSLMODE", "disable"),
		TimeZone: value("DB_TIMEZONE", "Asia/Shanghai"),
	}, nil
}

func parseDotEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "\"") {
		if !strings.HasSuffix(raw, "\"") || len(raw) == 1 {
			return "", fmt.Errorf("unterminated double-quoted value")
		}
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("invalid quoted value: %w", err)
		}
		return value, nil
	}
	if strings.HasPrefix(raw, "'") {
		if !strings.HasSuffix(raw, "'") || len(raw) == 1 {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	}
	return raw, nil
}

func validEnvKey(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for _, character := range value[1:] {
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}
