package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"HTTP_ADDR", "DB_PATH", "BUSINESS_TIMEZONE", "SESSION_TTL", "WORKER_POLL_INTERVAL",
		"WORKER_LEASE", "WORKER_MAX_ATTEMPTS", "BOOTSTRAP_OPERATOR_EMAIL", "BOOTSTRAP_OPERATOR_PASSWORD",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTP_ADDR", ":8081")
	t.Setenv("DB_PATH", "test.db")
	t.Setenv("BUSINESS_TIMEZONE", "Asia/Shanghai")
	t.Setenv("SESSION_TTL", "12h")
	t.Setenv("WORKER_POLL_INTERVAL", "1s")
	t.Setenv("WORKER_LEASE", "30s")
	t.Setenv("WORKER_MAX_ATTEMPTS", "5")
	t.Setenv("BOOTSTRAP_OPERATOR_EMAIL", " Operator@Example.Test ")
	t.Setenv("BOOTSTRAP_OPERATOR_PASSWORD", "change-me-now")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.HTTPAddr != ":8081" || cfg.DBPath != "test.db" {
		t.Fatalf("unexpected address/path: %+v", cfg)
	}
	if cfg.BusinessTimezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q", cfg.BusinessTimezone)
	}
	if cfg.SessionTTL != 12*time.Hour || cfg.WorkerPollInterval != time.Second || cfg.WorkerLease != 30*time.Second {
		t.Fatalf("unexpected durations: %+v", cfg)
	}
	if cfg.WorkerMaxAttempts != 5 {
		t.Fatalf("max attempts = %d", cfg.WorkerMaxAttempts)
	}
	if cfg.BootstrapOperatorEmail != "operator@example.test" {
		t.Fatalf("normalized email = %q", cfg.BootstrapOperatorEmail)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	base := map[string]string{
		"HTTP_ADDR": ":8080", "DB_PATH": "test.db", "BUSINESS_TIMEZONE": "UTC",
		"SESSION_TTL": "1h", "WORKER_POLL_INTERVAL": "1s", "WORKER_LEASE": "10s",
		"WORKER_MAX_ATTEMPTS": "3",
	}
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "bad timezone", key: "BUSINESS_TIMEZONE", value: "Mars/Olympus"},
		{name: "bad session duration", key: "SESSION_TTL", value: "tomorrow"},
		{name: "zero session", key: "SESSION_TTL", value: "0s"},
		{name: "negative poll", key: "WORKER_POLL_INTERVAL", value: "-1s"},
		{name: "zero lease", key: "WORKER_LEASE", value: "0s"},
		{name: "bad attempts", key: "WORKER_MAX_ATTEMPTS", value: "many"},
		{name: "zero attempts", key: "WORKER_MAX_ATTEMPTS", value: "0"},
		{name: "empty address", key: "HTTP_ADDR", value: ""},
		{name: "empty database", key: "DB_PATH", value: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range base {
				t.Setenv(key, value)
			}
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() unexpectedly succeeded")
			}
		})
	}
}
