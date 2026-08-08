// Package slack renders App Store Connect webhook events as Block Kit messages
// and posts them to Slack, either through an Incoming Webhook or through
// chat.postMessage.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.ngs.io/asc-slack-notifier/internal/webhook"
)

// defaultAPIBaseURL is the Slack Web API root used with a bot token.
const defaultAPIBaseURL = "https://slack.com/api"

// Options configures a Client. WebhookURL takes precedence when both an
// Incoming Webhook URL and a bot token are provided.
type Options struct {
	WebhookURL string
	BotToken   string
	Channel    string
	HTTPClient *http.Client
	// APIBaseURL overrides the Slack Web API root. Intended for tests.
	APIBaseURL string
}

// Client posts messages to Slack.
type Client struct {
	webhookURL string
	botToken   string
	channel    string
	apiBaseURL string
	httpClient *http.Client
}

// New builds a Client from opts. It returns an error when no destination is
// configured.
func New(opts Options) (*Client, error) {
	if opts.WebhookURL == "" && (opts.BotToken == "" || opts.Channel == "") {
		return nil, fmt.Errorf("slack: either WebhookURL or BotToken and Channel must be set")
	}
	c := &Client{
		webhookURL: opts.WebhookURL,
		botToken:   opts.BotToken,
		channel:    opts.Channel,
		apiBaseURL: opts.APIBaseURL,
		httpClient: opts.HTTPClient,
	}
	if c.apiBaseURL == "" {
		c.apiBaseURL = defaultAPIBaseURL
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return c, nil
}

// Notify renders the payload and posts it to Slack. It satisfies the notifier
// interface expected by the webhook handler.
func (c *Client) Notify(ctx context.Context, p *webhook.Payload) error {
	return c.Post(ctx, BuildMessage(p))
}

// Post sends an already rendered message to Slack.
func (c *Client) Post(ctx context.Context, msg *Message) error {
	if c.webhookURL != "" {
		return c.postIncomingWebhook(ctx, msg)
	}
	return c.postChatMessage(ctx, msg)
}

func (c *Client) postIncomingWebhook(ctx context.Context, msg *Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("slack: marshal message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: post incoming webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack: incoming webhook returned %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}
	return nil
}

func (c *Client) postChatMessage(ctx context.Context, msg *Message) error {
	withChannel := *msg
	withChannel.Channel = c.channel

	body, err := json.Marshal(&withChannel)
	if err != nil {
		return fmt.Errorf("slack: marshal message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: post chat.postMessage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack: chat.postMessage returned %d", resp.StatusCode)
	}

	var apiResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("slack: decode chat.postMessage response: %w", err)
	}
	if !apiResp.OK {
		return fmt.Errorf("slack: chat.postMessage failed: %s", orValue(apiResp.Error, "unknown error"))
	}
	return nil
}
