package config

import (
	"os"
	"strconv"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	S3Port      string
	UIPort      string
	AccessKeyID string
	SecretKey   string
	DataDir     string
	AutoBucket  bool
	Region      string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	cfg := Config{
		S3Port:      envOr("S3_PORT", "9000"),
		UIPort:      envOr("UI_PORT", "8025"),
		AccessKeyID: firstNonEmpty(envOr("ACCESS_KEY_ID", ""), os.Getenv("AWS_ACCESS_KEY_ID")),
		SecretKey:   firstNonEmpty(envOr("SECRET_ACCESS_KEY", ""), os.Getenv("AWS_SECRET_ACCESS_KEY")),
		DataDir:     envOr("S3_DATA_DIR", "/data"),
		AutoBucket:  envBool("AUTO_BUCKET", true),
		Region:      envOr("REGION", "us-east-1"),
	}
	if cfg.AccessKeyID == "" {
		cfg.AccessKeyID = "s3hog"
	}
	if cfg.SecretKey == "" {
		cfg.SecretKey = "s3hogsecret"
	}
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
