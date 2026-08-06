package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// MaxBodyBytes caps the accepted request body size.
const MaxBodyBytes = 1 << 20 // 1 MiB

// HealthPath answers load balancer and Cloud Run health checks.
const HealthPath = "/healthz"

// Notifier delivers a parsed webhook payload to a chat destination.
type Notifier interface {
	Notify(ctx context.Context, p *Payload) error
}

// Options configures a Handler.
type Options struct {
	// Secret is the shared HMAC-SHA256 secret. Required.
	Secret string
	// Path is the route notifications are posted to. Defaults to /webhook.
	Path string
	// Notifier receives every accepted payload. Required.
	Notifier Notifier
	// NotifyPing controls whether ping deliveries reach the notifier.
	NotifyPing bool
	// Logger defaults to slog.Default when nil.
	Logger *slog.Logger
}

// Handler serves the App Store Connect webhook endpoint and the health check.
type Handler struct {
	secret     string
	path       string
	notifier   Notifier
	notifyPing bool
	logger     *slog.Logger
}

// NewHandler builds the webhook HTTP handler.
func NewHandler(opts Options) (*Handler, error) {
	if opts.Secret == "" {
		return nil, errors.New("webhook: secret is required")
	}
	if opts.Notifier == nil {
		return nil, errors.New("webhook: notifier is required")
	}
	h := &Handler{
		secret:     opts.Secret,
		path:       opts.Path,
		notifier:   opts.Notifier,
		notifyPing: opts.NotifyPing,
		logger:     opts.Logger,
	}
	if h.path == "" {
		h.path = "/webhook"
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case HealthPath:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	case h.path:
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		h.handleWebhook(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		h.logger.Warn("failed to read request body", slog.Any("error", err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// The signature always covers the raw bytes exactly as received.
	if err := VerifyRequestSignature(h.secret, body, r.Header); err != nil {
		h.logger.Warn("signature verification failed", slog.Any("error", err))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	payload, err := Parse(body)
	if err != nil {
		h.logger.Warn("failed to parse payload", slog.Any("error", err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if payload.IsPing() {
		h.logger.Info("received ping", slog.String("type", payload.Data.Type))
		if !h.notifyPing {
			writeOK(w)
			return
		}
	} else if !isKnownEventType(payload.Data.Type) {
		h.logger.Warn("unknown event type; falling back to generic formatting",
			slog.String("type", payload.Data.Type))
	}

	if err := h.notifier.Notify(r.Context(), payload); err != nil {
		// Answer 5xx so App Store Connect redelivers the notification.
		h.logger.Error("failed to notify Slack",
			slog.String("type", payload.Data.Type), slog.Any("error", err))
		http.Error(w, "failed to deliver notification", http.StatusBadGateway)
		return
	}

	h.logger.Info("notification delivered",
		slog.String("type", payload.Data.Type), slog.String("event_id", payload.Data.ID))
	writeOK(w)
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
