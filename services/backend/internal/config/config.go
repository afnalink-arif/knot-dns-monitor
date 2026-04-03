package config

import (
	"os"
	"strings"
)

type Config struct {
	ServerPort    string
	PrometheusURL string
	ClickHouseURL string
	ClickHouseDB  string
	RedisURL      string
	PostgresURL   string
	JWTSecret     string
	CORSOrigins   []string
	Version       string
	ProjectDir    string
}

func Load() *Config {
	jwtSecret := envOrDefault("JWT_SECRET", "dev-secret-change-in-production")
	if file := os.Getenv("JWT_SECRET_FILE"); file != "" {
		if data, err := os.ReadFile(file); err == nil {
			jwtSecret = strings.TrimSpace(string(data))
		}
	}

	origins := envOrDefault("CORS_ORIGINS", "http://localhost:3000")

	return &Config{
		ServerPort:    envOrDefault("SERVER_PORT", "8080"),
		PrometheusURL: envOrDefault("PROMETHEUS_URL", "http://localhost:9090"),
		ClickHouseURL: envOrDefault("CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseDB:  envOrDefault("CLICKHOUSE_DB", "dnsmonitor"),
		RedisURL:      envOrDefault("REDIS_URL", "redis://localhost:6379"),
		PostgresURL:   envOrDefault("POSTGRES_URL", "postgres://dnsmon:password@localhost:5432/dnsmonitor?sslmode=disable"),
		JWTSecret:     jwtSecret,
		CORSOrigins:   strings.Split(origins, ","),
		ProjectDir:    envOrDefault("PROJECT_DIR", "/project"),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
