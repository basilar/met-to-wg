// Package config loads runtime configuration from environment variables.
//
// All secrets (Windguru UIDs/passwords, healthcheck URLs) come in as env vars
// — the binary itself is secret-blind. The recommended workflow is to keep
// them in a SOPS-encrypted file and run the service under `sops exec-env`.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the parsed runtime configuration.
type Config struct {
	DatabasePath    string
	WindguruBaseURL string // empty → default to upstream production
	HealthcheckURL  string // empty → ping disabled
	UserAgent       string
	Interval        time.Duration
	Concurrency     int
	FetchTimeout    time.Duration
	UploadTimeout   time.Duration

	Csopak        StationCreds
	Balatonfured  StationCreds
	Balatonalmadi StationCreds
}

// StationCreds is a station's Windguru identifier and shared secret.
type StationCreds struct {
	UID      string
	Password string
}

// HasAny reports whether any of the three stations has credentials configured.
// Used at startup to fail fast on an empty config rather than silently doing
// nothing every minute.
func (c *Config) HasAny() bool {
	return c.Csopak.UID != "" || c.Balatonfured.UID != "" || c.Balatonalmadi.UID != ""
}

// Load reads from os.Getenv. Missing required values cause an error; missing
// per-station credentials silently disable that station.
func Load() (*Config, error) {
	return LoadFromEnv(os.Getenv)
}

// LoadFromEnv is the testable form of Load — pass in a custom lookup func.
func LoadFromEnv(getenv func(string) string) (*Config, error) {
	c := &Config{
		DatabasePath:    getenv("DATABASE_PATH"),
		WindguruBaseURL: getenv("WINDGURU_BASE_URL"),
		HealthcheckURL:  getenv("HEALTHCHECK_URL"),
		UserAgent:       getenv("USER_AGENT"),

		Csopak: StationCreds{
			UID: getenv("CSOPAK_WEATHER_UID"), Password: getenv("CSOPAK_WEATHER_API_PASSWORD"),
		},
		Balatonfured: StationCreds{
			UID: getenv("FURED_WEATHER_UID"), Password: getenv("FURED_WEATHER_API_PASSWORD"),
		},
		Balatonalmadi: StationCreds{
			UID: getenv("ALMADI_WEATHER_UID"), Password: getenv("ALMADI_WEATHER_API_PASSWORD"),
		},
	}
	if c.DatabasePath == "" {
		return nil, errors.New("DATABASE_PATH is required")
	}
	if c.UserAgent == "" {
		c.UserAgent = "met-to-wg/1.0"
	}

	var err error
	if c.Interval, err = parseDuration(getenv, "INTERVAL", 60*time.Second); err != nil {
		return nil, err
	}
	if c.FetchTimeout, err = parseDuration(getenv, "FETCH_TIMEOUT", 15*time.Second); err != nil {
		return nil, err
	}
	if c.UploadTimeout, err = parseDuration(getenv, "UPLOAD_TIMEOUT", 15*time.Second); err != nil {
		return nil, err
	}
	if c.Concurrency, err = parseInt(getenv, "CONCURRENCY", 2); err != nil {
		return nil, err
	}

	if !c.HasAny() {
		return nil, errors.New("no station credentials configured: at least one of CSOPAK_WEATHER_UID, FURED_WEATHER_UID, ALMADI_WEATHER_UID must be set")
	}
	return c, nil
}

func parseDuration(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: must be positive", key)
	}
	return d, nil
}

func parseInt(getenv func(string) string, key string, def int) (int, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: must be positive", key)
	}
	return n, nil
}
