package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr                  string
	DBPath                    string
	BusinessTimezone          string
	SessionTTL                time.Duration
	WorkerPollInterval        time.Duration
	WorkerLease               time.Duration
	WorkerMaxAttempts         int
	BootstrapOperatorEmail    string
	BootstrapOperatorPassword string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:                  env("HTTP_ADDR", ":8080"),
		DBPath:                    env("DB_PATH", "venue-slot.db"),
		BusinessTimezone:          env("BUSINESS_TIMEZONE", "Asia/Shanghai"),
		BootstrapOperatorEmail:    strings.TrimSpace(strings.ToLower(env("BOOTSTRAP_OPERATOR_EMAIL", "operator@example.test"))),
		BootstrapOperatorPassword: env("BOOTSTRAP_OPERATOR_PASSWORD", "change-me-now"),
	}
	var err error
	if cfg.SessionTTL, err = durationEnv("SESSION_TTL", 12*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.WorkerPollInterval, err = durationEnv("WORKER_POLL_INTERVAL", time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WorkerLease, err = durationEnv("WORKER_LEASE", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WorkerMaxAttempts, err = intEnv("WORKER_MAX_ATTEMPTS", 5); err != nil {
		return Config{}, err
	}
	if _, err := time.LoadLocation(cfg.BusinessTimezone); err != nil {
		return Config{}, fmt.Errorf("business timezone: %w", err)
	}
	if cfg.HTTPAddr == "" || cfg.DBPath == "" {
		return Config{}, errors.New("HTTP_ADDR and DB_PATH must not be empty")
	}
	if cfg.SessionTTL <= 0 || cfg.WorkerPollInterval <= 0 || cfg.WorkerLease <= 0 {
		return Config{}, errors.New("durations must be positive")
	}
	if cfg.WorkerMaxAttempts < 1 {
		return Config{}, errors.New("WORKER_MAX_ATTEMPTS must be positive")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := env(key, fallback.String())
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func intEnv(key string, fallback int) (int, error) {
	raw := env(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}
