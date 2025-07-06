package config

import (
	"os"
	"strconv"
)

type Config struct {
	Environment string
	Server      ServerConfig
	Database    DatabaseConfig
	SSL         SSLConfig
	Auth        AuthConfig
}

type ServerConfig struct {
	Address string
	Port    int
}

type DatabaseConfig struct {
	Path string
}

type SSLConfig struct {
	Enabled    bool
	Domain     string
	CacheDir   string
	Email      string
}

type AuthConfig struct {
	JWTSecret     string
	TokenExpiry   int // hours
	BCryptCost    int
}

func Load() (*Config, error) {
	cfg := &Config{
		Environment: getEnv("ENVIRONMENT", "development"),
		Server: ServerConfig{
			Address: getEnv("SERVER_ADDRESS", ":8080"),
			Port:    getEnvInt("SERVER_PORT", 8080),
		},
		Database: DatabaseConfig{
			Path: getEnv("DATABASE_PATH", "./sdr.db"),
		},
		SSL: SSLConfig{
			Enabled:  getEnvBool("SSL_ENABLED", false),
			Domain:   getEnv("SSL_DOMAIN", ""),
			CacheDir: getEnv("SSL_CACHE_DIR", "./certs"),
			Email:    getEnv("SSL_EMAIL", ""),
		},
		Auth: AuthConfig{
			JWTSecret:   getEnv("JWT_SECRET", "your-secret-key-change-in-production"),
			TokenExpiry: getEnvInt("TOKEN_EXPIRY_HOURS", 24),
			BCryptCost:  getEnvInt("BCRYPT_COST", 12),
		},
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}