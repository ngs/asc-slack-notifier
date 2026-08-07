// Command asc-slack-notifier receives App Store Connect webhook notifications
// and posts them to Slack. The same http.Handler is served either by a plain
// HTTP server (Cloud Run) or through the AWS Lambda runtime, selected by
// RUN_MODE.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"go.ngs.io/asc-slack-notifier/internal/config"
	"go.ngs.io/asc-slack-notifier/internal/slack"
	"go.ngs.io/asc-slack-notifier/internal/webhook"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", slog.Any("error", err))
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	slackClient, err := slack.New(slack.Options{
		WebhookURL: cfg.SlackWebhookURL,
		BotToken:   cfg.SlackBotToken,
		Channel:    cfg.SlackChannel,
	})
	if err != nil {
		logger.Error("failed to build Slack client", slog.Any("error", err))
		os.Exit(1)
	}

	handler, err := webhook.NewHandler(webhook.Options{
		Secret:     cfg.Secret,
		Path:       cfg.WebhookPath,
		HealthPath: cfg.HealthPath,
		Notifier:   slackClient,
		NotifyPing: cfg.NotifyPing,
		Logger:     logger,
	})
	if err != nil {
		logger.Error("failed to build webhook handler", slog.Any("error", err))
		os.Exit(1)
	}

	switch cfg.Mode {
	case config.ModeLambda:
		logger.Info("starting in lambda mode",
			slog.String("path", cfg.WebhookPath), slog.String("health_path", cfg.HealthPath))
		lambda.Start(httpadapter.NewV2(handler).ProxyWithContext)
	default:
		if err := serveHTTP(handler, cfg, logger); err != nil {
			logger.Error("http server terminated", slog.Any("error", err))
			os.Exit(1)
		}
	}
}

// serveHTTP runs the handler as a normal HTTP server and shuts it down
// gracefully on SIGINT/SIGTERM.
func serveHTTP(handler http.Handler, cfg *config.Config, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting in http mode",
			slog.String("addr", srv.Addr), slog.String("path", cfg.WebhookPath),
			slog.String("health_path", cfg.HealthPath))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
