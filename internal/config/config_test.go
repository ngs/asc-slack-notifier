package config

import (
	"errors"
	"log/slog"
	"testing"
)

// clearEnv unsets every variable Load reads so that ambient values in the
// developer's shell cannot influence the result.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ASC_WEBHOOK_SECRET", "SLACK_WEBHOOK_URL", "SLACK_BOT_TOKEN", "SLACK_CHANNEL",
		"RUN_MODE", "AWS_LAMBDA_FUNCTION_NAME", "PORT", "WEBHOOK_PATH", "HEALTH_PATH",
		"NOTIFY_PING", "LOG_LEVEL",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != ModeHTTP {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeHTTP)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.WebhookPath != "/webhook" {
		t.Errorf("WebhookPath = %q, want /webhook", cfg.WebhookPath)
	}
	if cfg.HealthPath != "/health" {
		t.Errorf("HealthPath = %q, want /health", cfg.HealthPath)
	}
	if !cfg.NotifyPing {
		t.Error("NotifyPing = false, want true")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestLoadRequiresSecret(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")

	if _, err := Load(); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("Load error = %v, want %v", err, ErrNoSecret)
	}
}

func TestLoadRequiresSlackDestination(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-token")
	t.Setenv("SLACK_CHANNEL", "")

	if _, err := Load(); !errors.Is(err, ErrNoSlackDestination) {
		t.Fatalf("Load error = %v, want %v", err, ErrNoSlackDestination)
	}
}

func TestLoadBotTokenDestination(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-token")
	t.Setenv("SLACK_CHANNEL", "#releases")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SlackBotToken != "xoxb-token" || cfg.SlackChannel != "#releases" {
		t.Errorf("bot token destination = %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
	t.Setenv("PORT", "9090")
	t.Setenv("WEBHOOK_PATH", "asc")
	t.Setenv("HEALTH_PATH", "healthz")
	t.Setenv("NOTIFY_PING", "false")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q", cfg.Port)
	}
	if cfg.WebhookPath != "/asc" {
		t.Errorf("WebhookPath = %q, want a leading slash to be added", cfg.WebhookPath)
	}
	if cfg.HealthPath != "/healthz" {
		t.Errorf("HealthPath = %q, want a leading slash to be added", cfg.HealthPath)
	}
	if cfg.NotifyPing {
		t.Error("NotifyPing = true, want false")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
	t.Setenv("LOG_LEVEL", "verbose")

	if _, err := Load(); err == nil {
		t.Fatal("Load error = nil, want error")
	}
}

func TestLoadRejectsCollidingPaths(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
	t.Setenv("WEBHOOK_PATH", "/hook")
	t.Setenv("HEALTH_PATH", "/hook")

	if _, err := Load(); err == nil {
		t.Fatal("Load error = nil, want an error for colliding paths")
	}
}

func TestResolveMode(t *testing.T) {
	clearEnv(t)
	tests := []struct {
		name       string
		runMode    string
		lambdaName string
		want       RunMode
	}{
		{name: "explicit http", runMode: "http", lambdaName: "fn", want: ModeHTTP},
		{name: "explicit lambda", runMode: "lambda", want: ModeLambda},
		{name: "case insensitive", runMode: "Lambda", want: ModeLambda},
		{name: "auto detect lambda", lambdaName: "asc-slack-notifier", want: ModeLambda},
		{name: "auto detect http", want: ModeHTTP},
		{name: "unknown value falls back to auto detect", runMode: "cloudrun", want: ModeHTTP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RUN_MODE", tt.runMode)
			t.Setenv("AWS_LAMBDA_FUNCTION_NAME", tt.lambdaName)
			if got := resolveMode(); got != tt.want {
				t.Fatalf("resolveMode = %q, want %q", got, tt.want)
			}
		})
	}
}
