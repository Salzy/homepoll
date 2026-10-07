package config

import (
	"fmt"
	"net/url"
	"os"
)

// Config holds the runtime configuration loaded from environment variables.
type Config struct {
	DryRun       bool
	DSN          string
	ETABaseURL   string
	PowerBaseURL string
	PowerToken   string
	PowerGPNR    string
	PowerZP      string
	MCPAddr      string
	MCPToken     string
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		DryRun: env("DRY_RUN", "false") == "true",
		DSN:    dsn(),
		ETABaseURL: fmt.Sprintf(
			"http://%s:%s",
			env("ETA_HOST", "192.168.1.142"),
			env("ETA_PORT", "8080"),
		),
		PowerBaseURL: env("POWER_HOST", "https://api.salzburgnetz.at"),
		PowerToken:   env("POWER_TOKEN", ""),
		PowerGPNR:    env("POWER_GPNR", ""),
		PowerZP:      env("POWER_ZP", ""),
		MCPAddr:      env("MCP_ADDR", ""),
		MCPToken:     env("MCP_TOKEN", ""),
	}
}

// dsn builds the PostgreSQL connection string from the POSTGRES_* variables.
func dsn() string {
	u := url.URL{
		Scheme: "postgres",
		User: url.UserPassword(
			env("POSTGRES_USER", "homepoll"),
			env("POSTGRES_PASSWORD", "homepoll"),
		),
		Host: fmt.Sprintf(
			"%s:%s",
			env("POSTGRES_HOST", "localhost"),
			env("POSTGRES_PORT", "5432"),
		),
		Path: "/" + env("POSTGRES_DB", "homepoll"),
	}
	q := u.Query()
	q.Set("sslmode", env("POSTGRES_SSLMODE", "disable"))
	u.RawQuery = q.Encode()
	return u.String()
}

// env returns the value of the named environment variable, or def if unset.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
