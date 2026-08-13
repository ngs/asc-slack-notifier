// Package config loads and validates the service configuration from the
// process environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// RunMode selects how the HTTP handler is served.
type RunMode string

const (
	// ModeHTTP serves the handler with a plain net/http server (Cloud Run).
	ModeHTTP RunMode = "http"
	// ModeLambda serves the handler through the AWS Lambda runtime.
	ModeLambda RunMode = "lambda"
)

// Config holds every runtime setting of the service.
type Config struct {
	// Mode decides how the handler is served.
	Mode RunMode
	// Port is the listen port used in ModeHTTP.
	Port string
	// WebhookPath is the path App Store Connect posts notifications to.
	WebhookPath string
	// HealthPath is the path answering health checks.
	HealthPath string
	// Secret is the shared HMAC-SHA256 secret registered with the webhook.
	Secret string
	// SlackWebhookURL is a Slack Incoming Webhook URL. Takes precedence over
	// the bot token when both are configured.
	SlackWebhookURL string
	// SlackBotToken is a Slack bot token used with chat.postMessage.
	SlackBotToken string
	// SlackChannel is the target channel for chat.postMessage.
	SlackChannel string
	// NotifyPing controls whether ping events produce a Slack message.
	NotifyPing bool
	// LogLevel is the minimum slog level emitted.
	LogLevel slog.Level
	// ASCAPIKeyID is the App Store Connect API key ID. When empty, message
	// enrichment is disabled.
	ASCAPIKeyID string
	// ASCAPIIssuerID is the App Store Connect API issuer ID.
	ASCAPIIssuerID string
	// ASCAPIPrivateKey is the PEM encoded private key of the API key.
	ASCAPIPrivateKey []byte
}

// EnrichmentEnabled reports whether App Store Connect API credentials are
// configured.
func (c *Config) EnrichmentEnabled() bool { return c.ASCAPIKeyID != "" }

// ErrNoSlackDestination is returned when neither an Incoming Webhook URL nor a
// bot token and channel pair is configured.
var ErrNoSlackDestination = errors.New("no Slack destination configured: set SLACK_WEBHOOK_URL or SLACK_BOT_TOKEN and SLACK_CHANNEL")

// ErrNoSecret is returned when the HMAC secret is missing.
var ErrNoSecret = errors.New("ASC_WEBHOOK_SECRET is required")

// Load reads the configuration from the environment and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Mode:            resolveMode(),
		Port:            envOr("PORT", "8080"),
		WebhookPath:     normalizePath(envOr("WEBHOOK_PATH", "/webhook")),
		HealthPath:      normalizePath(envOr("HEALTH_PATH", "/health")),
		Secret:          os.Getenv("ASC_WEBHOOK_SECRET"),
		SlackWebhookURL: strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL")),
		SlackBotToken:   strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN")),
		SlackChannel:    strings.TrimSpace(os.Getenv("SLACK_CHANNEL")),
		NotifyPing:      envBool("NOTIFY_PING", true),
	}

	level, err := parseLogLevel(envOr("LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	if cfg.Secret == "" {
		return nil, ErrNoSecret
	}
	if cfg.SlackWebhookURL == "" && (cfg.SlackBotToken == "" || cfg.SlackChannel == "") {
		return nil, ErrNoSlackDestination
	}
	if cfg.HealthPath == cfg.WebhookPath {
		return nil, fmt.Errorf("HEALTH_PATH and WEBHOOK_PATH must differ (both are %q)", cfg.HealthPath)
	}
	if err := loadASCAPI(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadASCAPI reads the optional App Store Connect API credentials. The three
// settings form a group: none set disables enrichment, a partial set is a
// configuration error rather than a silent downgrade.
func loadASCAPI(cfg *Config) error {
	keyID := strings.TrimSpace(os.Getenv("ASC_API_KEY_ID"))
	issuerID := strings.TrimSpace(os.Getenv("ASC_API_ISSUER_ID"))
	key := strings.TrimSpace(os.Getenv("ASC_API_PRIVATE_KEY"))
	keyPath := strings.TrimSpace(os.Getenv("ASC_API_PRIVATE_KEY_PATH"))

	if keyID == "" && issuerID == "" && key == "" && keyPath == "" {
		return nil
	}

	var missing []string
	if keyID == "" {
		missing = append(missing, "ASC_API_KEY_ID")
	}
	if issuerID == "" {
		missing = append(missing, "ASC_API_ISSUER_ID")
	}
	if key == "" && keyPath == "" {
		missing = append(missing, "ASC_API_PRIVATE_KEY or ASC_API_PRIVATE_KEY_PATH")
	}
	if len(missing) > 0 {
		return fmt.Errorf("incomplete App Store Connect API configuration: missing %s",
			strings.Join(missing, ", "))
	}

	keyPEM, err := decodePrivateKey(key)
	if err != nil {
		return err
	}
	if key == "" {
		b, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("read ASC_API_PRIVATE_KEY_PATH: %w", err)
		}
		keyPEM = b
	}

	cfg.ASCAPIKeyID = keyID
	cfg.ASCAPIIssuerID = issuerID
	cfg.ASCAPIPrivateKey = keyPEM
	return nil
}

// decodePrivateKey turns the ASC_API_PRIVATE_KEY value into PEM bytes. Like
// fastlane's key_content, the value may be the PEM itself (with literal
// backslash-n sequences accepted as line breaks, since a PEM key does not
// survive a single-line environment variable) or the whole PEM base64 encoded.
// The two are told apart by the PEM armor: a base64 encoding never contains
// "-----BEGIN".
func decodePrivateKey(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	expanded := strings.ReplaceAll(v, `\n`, "\n")
	if strings.Contains(expanded, "-----BEGIN") {
		return []byte(expanded), nil
	}
	compact := strings.Join(strings.Fields(v), "")
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("ASC_API_PRIVATE_KEY is neither PEM nor base64-encoded PEM: %w", err)
	}
	if !strings.Contains(string(decoded), "-----BEGIN") {
		return nil, errors.New("ASC_API_PRIVATE_KEY decoded from base64 but the result is not PEM")
	}
	return decoded, nil
}

// resolveMode obeys RUN_MODE when it names a known mode, and otherwise detects
// the Lambda runtime from the environment injected by AWS.
func resolveMode() RunMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RUN_MODE"))) {
	case string(ModeHTTP):
		return ModeHTTP
	case string(ModeLambda):
		return ModeLambda
	}
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		return ModeLambda
	}
	return ModeHTTP
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid LOG_LEVEL %q", s)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "0", "f", "false", "no", "off":
		return false
	default:
		return true
	}
}

func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
