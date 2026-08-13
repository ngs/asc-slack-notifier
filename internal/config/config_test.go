package config

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
		"ASC_API_KEY_ID", "ASC_API_ISSUER_ID", "ASC_API_PRIVATE_KEY", "ASC_API_PRIVATE_KEY_PATH",
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

const testKeyPEM = "-----BEGIN PRIVATE KEY-----\nMIGHAgEA\n-----END PRIVATE KEY-----\n"

func TestLoadASCAPIDisabledByDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EnrichmentEnabled() {
		t.Error("EnrichmentEnabled = true with no ASC API variables set")
	}
	if cfg.ASCAPIPrivateKey != nil {
		t.Errorf("ASCAPIPrivateKey = %q, want nil", cfg.ASCAPIPrivateKey)
	}
}

func TestLoadASCAPIFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
	t.Setenv("ASC_API_KEY_ID", "KEYID123")
	t.Setenv("ASC_API_ISSUER_ID", "issuer-uuid")
	// Single-line value, as a secret manager or CI variable delivers it.
	t.Setenv("ASC_API_PRIVATE_KEY", strings.ReplaceAll(testKeyPEM, "\n", `\n`))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.EnrichmentEnabled() {
		t.Error("EnrichmentEnabled = false, want true")
	}
	if cfg.ASCAPIKeyID != "KEYID123" || cfg.ASCAPIIssuerID != "issuer-uuid" {
		t.Errorf("key/issuer = %q/%q", cfg.ASCAPIKeyID, cfg.ASCAPIIssuerID)
	}
	if got := string(cfg.ASCAPIPrivateKey); got != testKeyPEM {
		t.Errorf("private key = %q, want the literal \\n sequences expanded", got)
	}
}

func TestLoadASCAPIFromBase64Env(t *testing.T) {
	clearEnv(t)
	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
	t.Setenv("ASC_API_KEY_ID", "KEYID123")
	t.Setenv("ASC_API_ISSUER_ID", "issuer-uuid")
	// base64 of the whole PEM, wrapped the way the base64 command emits it.
	encoded := base64.StdEncoding.EncodeToString([]byte(testKeyPEM))
	t.Setenv("ASC_API_PRIVATE_KEY", encoded[:20]+"\n"+encoded[20:])

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := string(cfg.ASCAPIPrivateKey); got != testKeyPEM {
		t.Errorf("private key = %q, want the base64-decoded PEM", got)
	}
}

func TestLoadASCAPIFromFile(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "AuthKey.p8")
	if err := os.WriteFile(path, []byte(testKeyPEM), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
	t.Setenv("ASC_API_KEY_ID", "KEYID123")
	t.Setenv("ASC_API_ISSUER_ID", "issuer-uuid")
	t.Setenv("ASC_API_PRIVATE_KEY_PATH", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := string(cfg.ASCAPIPrivateKey); got != testKeyPEM {
		t.Errorf("private key = %q, want the file contents", got)
	}
}

func TestLoadASCAPIErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "key ID only", env: map[string]string{"ASC_API_KEY_ID": "KEYID123"}},
		{name: "no issuer", env: map[string]string{
			"ASC_API_KEY_ID": "KEYID123", "ASC_API_PRIVATE_KEY": testKeyPEM,
		}},
		{name: "no private key", env: map[string]string{
			"ASC_API_KEY_ID": "KEYID123", "ASC_API_ISSUER_ID": "issuer-uuid",
		}},
		{name: "unreadable key file", env: map[string]string{
			"ASC_API_KEY_ID": "KEYID123", "ASC_API_ISSUER_ID": "issuer-uuid",
			"ASC_API_PRIVATE_KEY_PATH": "/nonexistent/AuthKey.p8",
		}},
		{name: "key neither PEM nor base64", env: map[string]string{
			"ASC_API_KEY_ID": "KEYID123", "ASC_API_ISSUER_ID": "issuer-uuid",
			"ASC_API_PRIVATE_KEY": "not*valid*base64",
		}},
		{name: "base64 of non-PEM", env: map[string]string{
			"ASC_API_KEY_ID": "KEYID123", "ASC_API_ISSUER_ID": "issuer-uuid",
			"ASC_API_PRIVATE_KEY": "bm90IGEgUEVNIGtleQ==",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("ASC_WEBHOOK_SECRET", "s3cret")
			t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/x")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load error = nil, want an error")
			}
		})
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
