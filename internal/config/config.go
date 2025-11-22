// Package config provides centralized configuration management with
// environment variable support for secure credential handling.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all application configuration
type Config struct {
	Database DatabaseConfig
	Bluesky  BlueskyConfig
	Server   ServerConfig
	Polling  PollingConfig
	Cleanup  CleanupConfig
}

// DatabaseConfig holds database connection settings
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// BlueskyConfig holds Bluesky API credentials
type BlueskyConfig struct {
	Handle   string
	Password string
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Host            string
	Port            int
	TLSCertFile     string
	TLSKeyFile      string
	CORSAllowOrigin string
	RateLimitRPM    int // Requests per minute
}

// PollingConfig holds polling settings
type PollingConfig struct {
	IntervalMinutes      int
	PostsPerPage         int
	MaxConcurrent        int
	RateLimitMs          int
	InitialLookbackHours int
	MaxRetries           int
	RetryBackoffMs       int
	MaxPagesPerUser      int
}

// CleanupConfig holds cleanup settings
type CleanupConfig struct {
	RetentionHours       int
	CleanupIntervalMin   int
	TrendingThreshold    int
	CursorUpdateSeconds  int
}

// Load reads configuration from file and environment variables.
// Environment variables take precedence over config file values.
// Sensitive values (passwords) should ONLY be set via environment variables in production.
func Load() (*Config, error) {
	// Set up viper
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	// Enable environment variable support
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Bind specific environment variables for nested config
	bindEnvVars()

	// Read config file (optional in production - can use env vars only)
	if err := viper.ReadInConfig(); err != nil {
		// Config file is optional if env vars are set
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Build config struct
	cfg := &Config{
		Database: DatabaseConfig{
			Host:     getStringWithEnvFallback("database.host", "DB_HOST", "localhost"),
			Port:     getIntWithEnvFallback("database.port", "DB_PORT", 5432),
			User:     getStringWithEnvFallback("database.user", "DB_USER", "postgres"),
			Password: getStringWithEnvFallback("database.password", "DB_PASSWORD", ""),
			DBName:   getStringWithEnvFallback("database.dbname", "DB_NAME", "bluesky_news"),
			SSLMode:  getStringWithEnvFallback("database.sslmode", "DB_SSLMODE", "disable"),
		},
		Bluesky: BlueskyConfig{
			Handle:   getStringWithEnvFallback("bluesky.handle", "BLUESKY_HANDLE", ""),
			Password: getStringWithEnvFallback("bluesky.password", "BLUESKY_PASSWORD", ""),
		},
		Server: ServerConfig{
			Host:            getStringWithEnvFallback("server.host", "SERVER_HOST", "0.0.0.0"),
			Port:            getIntWithEnvFallback("server.port", "SERVER_PORT", 8080),
			TLSCertFile:     getStringWithEnvFallback("server.tls_cert", "TLS_CERT_FILE", ""),
			TLSKeyFile:      getStringWithEnvFallback("server.tls_key", "TLS_KEY_FILE", ""),
			CORSAllowOrigin: getStringWithEnvFallback("server.cors_origin", "CORS_ALLOW_ORIGIN", "*"),
			RateLimitRPM:    getIntWithEnvFallback("server.rate_limit_rpm", "RATE_LIMIT_RPM", 100),
		},
		Polling: PollingConfig{
			IntervalMinutes:      viper.GetInt("polling.interval_minutes"),
			PostsPerPage:         viper.GetInt("polling.posts_per_page"),
			MaxConcurrent:        viper.GetInt("polling.max_concurrent"),
			RateLimitMs:          viper.GetInt("polling.rate_limit_ms"),
			InitialLookbackHours: viper.GetInt("polling.initial_lookback_hours"),
			MaxRetries:           viper.GetInt("polling.max_retries"),
			RetryBackoffMs:       viper.GetInt("polling.retry_backoff_ms"),
			MaxPagesPerUser:      viper.GetInt("polling.max_pages_per_user"),
		},
		Cleanup: CleanupConfig{
			RetentionHours:      getIntWithEnvFallback("cleanup.retention_hours", "CLEANUP_RETENTION_HOURS", 24),
			CleanupIntervalMin:  getIntWithEnvFallback("cleanup.cleanup_interval_minutes", "CLEANUP_INTERVAL_MIN", 60),
			TrendingThreshold:   getIntWithEnvFallback("cleanup.trending_threshold", "CLEANUP_TRENDING_THRESHOLD", 5),
			CursorUpdateSeconds: getIntWithEnvFallback("cleanup.cursor_update_seconds", "CURSOR_UPDATE_SECONDS", 10),
		},
	}

	// Set defaults for polling if not configured
	if cfg.Polling.IntervalMinutes == 0 {
		cfg.Polling.IntervalMinutes = 15
	}
	if cfg.Polling.PostsPerPage == 0 {
		cfg.Polling.PostsPerPage = 50
	}
	if cfg.Polling.MaxConcurrent == 0 {
		cfg.Polling.MaxConcurrent = 10
	}
	if cfg.Polling.RateLimitMs == 0 {
		cfg.Polling.RateLimitMs = 100
	}
	if cfg.Polling.InitialLookbackHours == 0 {
		cfg.Polling.InitialLookbackHours = 24
	}
	if cfg.Polling.MaxRetries == 0 {
		cfg.Polling.MaxRetries = 3
	}
	if cfg.Polling.RetryBackoffMs == 0 {
		cfg.Polling.RetryBackoffMs = 1000
	}
	if cfg.Polling.MaxPagesPerUser == 0 {
		cfg.Polling.MaxPagesPerUser = 100
	}

	return cfg, nil
}

// DatabaseConnString returns a PostgreSQL connection string.
// This method intentionally does NOT log the password.
func (c *DatabaseConfig) DatabaseConnString() string {
	if c.Password == "" {
		return fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s",
			c.Host, c.Port, c.User, c.DBName, c.SSLMode,
		)
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
	)
}

// DatabaseConnStringSafe returns a connection string with password redacted for logging
func (c *DatabaseConfig) DatabaseConnStringSafe() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.DBName, c.SSLMode,
	)
}

// IsTLSEnabled returns true if TLS certificate and key are configured
func (c *ServerConfig) IsTLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// bindEnvVars explicitly binds environment variables to viper keys
func bindEnvVars() {
	// Database
	viper.BindEnv("database.host", "DB_HOST")
	viper.BindEnv("database.port", "DB_PORT")
	viper.BindEnv("database.user", "DB_USER")
	viper.BindEnv("database.password", "DB_PASSWORD")
	viper.BindEnv("database.dbname", "DB_NAME")
	viper.BindEnv("database.sslmode", "DB_SSLMODE")

	// Bluesky
	viper.BindEnv("bluesky.handle", "BLUESKY_HANDLE")
	viper.BindEnv("bluesky.password", "BLUESKY_PASSWORD")

	// Server
	viper.BindEnv("server.host", "SERVER_HOST")
	viper.BindEnv("server.port", "SERVER_PORT")
	viper.BindEnv("server.tls_cert", "TLS_CERT_FILE")
	viper.BindEnv("server.tls_key", "TLS_KEY_FILE")
	viper.BindEnv("server.cors_origin", "CORS_ALLOW_ORIGIN")
	viper.BindEnv("server.rate_limit_rpm", "RATE_LIMIT_RPM")
}

// getStringWithEnvFallback gets a string value, preferring env var over config file
func getStringWithEnvFallback(viperKey, envKey, defaultVal string) string {
	// Check environment variable first
	if val := os.Getenv(envKey); val != "" {
		return val
	}
	// Then check viper (config file)
	if val := viper.GetString(viperKey); val != "" {
		return val
	}
	return defaultVal
}

// getIntWithEnvFallback gets an int value, preferring env var over config file
func getIntWithEnvFallback(viperKey, envKey string, defaultVal int) int {
	// Check environment variable first
	if val := os.Getenv(envKey); val != "" {
		var intVal int
		fmt.Sscanf(val, "%d", &intVal)
		if intVal != 0 {
			return intVal
		}
	}
	// Then check viper (config file)
	if val := viper.GetInt(viperKey); val != 0 {
		return val
	}
	return defaultVal
}
