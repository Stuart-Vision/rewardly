// Package config reads all settings from environment variables (12-factor).
package config

import (
	"errors"
	"os"
)

type Config struct {
	Port        string
	DatabaseURL string
	CORSOrigin  string
}

func Load() (Config, error) {
	c := Config{
		Port:        getenv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		CORSOrigin:  getenv("CORS_ORIGIN", "*"),
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
