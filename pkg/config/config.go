package config

import (
	"os"
	"strconv"
)

type Config struct {
	// Common
	Mode        string `env:"MODE" default:"api"`
	Environment string
	LogLevel    string `env:"LOG_LEVEL" default:"info"`

	// Mode-specific configs
	Server    ServerConfig
	Database  DatabaseConfig
	SSL       SSLConfig
	Auth      AuthConfig
	Collector CollectorConfig
	Receiver  ReceiverConfig
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

type CollectorConfig struct {
	StationID       string `env:"STATION_ID"`
	DataDir         string `env:"DATA_DIR" default:"./nice_data"`
	ContainerImage  string `env:"CONTAINER_IMAGE" default:"argussdr/sdr-tdoa-df:release-0.3"`
	APIServerURL    string `env:"API_SERVER_URL"`
}

type ReceiverConfig struct {
	ReceiverID   string `env:"RECEIVER_ID"`
	DownloadDir  string `env:"DOWNLOAD_DIR" default:"./downloads"`
	APIServerURL string `env:"API_SERVER_URL"`
}

func Load() (*Config, error) {
	cfg := &Config{
		// Common
		Mode:        getEnv("MODE", "api"),
		Environment: getEnv("ENVIRONMENT", "development"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		// API Server
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

		// Collector Client
		Collector: CollectorConfig{
			StationID:      getEnv("STATION_ID", ""),
			DataDir:        getEnv("DATA_DIR", "./nice_data"),
			ContainerImage: getEnv("CONTAINER_IMAGE", "argussdr/sdr-tdoa-df:release-0.4"),
			APIServerURL:   getEnv("API_SERVER_URL", "http://localhost:8080"),
		},

		// Receiver Client
		Receiver: ReceiverConfig{
			ReceiverID:   getEnv("RECEIVER_ID", ""),
			DownloadDir:  getEnv("DOWNLOAD_DIR", "./downloads"),
			APIServerURL: getEnv("API_SERVER_URL", "http://localhost:8080"),
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
