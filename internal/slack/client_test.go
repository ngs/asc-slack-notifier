package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.ngs.io/asc-slack-notifier/internal/webhook"
)

func TestNewRequiresDestination(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("New with no destination: error = nil, want error")
	}
	if _, err := New(Options{BotToken: "xoxb-token"}); err == nil {
		t.Error("New with a token but no channel: error = nil, want error")
	}
	if _, err := New(Options{WebhookURL: "https://hooks.slack.com/services/x"}); err != nil {
		t.Errorf("New with a webhook URL: %v", err)
	}
}

func TestNotifyViaIncomingWebhook(t *testing.T) {
	var got struct {
		body        []byte
		contentType string
		method      string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.body, _ = io.ReadAll(r.Body)
		got.contentType = r.Header.Get("Content-Type")
		got.method = r.Method
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c, err := New(Options{WebhookURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	p, err := webhook.Parse([]byte(`{"data":{"type":"buildUploadStateUpdated","id":"1","attributes":{"oldValue":"PROCESSING","newValue":"COMPLETE"}}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := c.Notify(context.Background(), p); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q", got.contentType)
	}

	var sent Message
	if err := json.Unmarshal(got.body, &sent); err != nil {
		t.Fatalf("unmarshal posted body: %v", err)
	}
	if sent.Channel != "" {
		t.Errorf("channel = %q, want empty for an Incoming Webhook", sent.Channel)
	}
	if len(sent.Blocks) == 0 {
		t.Error("posted message carries no blocks")
	}
}

func TestIncomingWebhookErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "server error")
	}))
	defer srv.Close()

	c, err := New(Options{WebhookURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Post(context.Background(), &Message{Text: "hi"}); err == nil {
		t.Fatal("Post error = nil, want error")
	}
}

func TestPostChatMessage(t *testing.T) {
	var gotAuth string
	var sent Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("path = %q, want /chat.postMessage", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c, err := New(Options{
		BotToken:   "xoxb-token",
		Channel:    "#releases",
		APIBaseURL: srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Post(context.Background(), &Message{Text: "hi"}); err != nil {
		t.Fatalf("Post: %v", err)
	}

	if gotAuth != "Bearer xoxb-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if sent.Channel != "#releases" {
		t.Errorf("channel = %q, want #releases", sent.Channel)
	}
}

func TestPostChatMessageAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":false,"error":"channel_not_found"}`)
	}))
	defer srv.Close()

	c, err := New(Options{
		BotToken:   "xoxb-token",
		Channel:    "#missing",
		APIBaseURL: srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = c.Post(context.Background(), &Message{Text: "hi"})
	if err == nil {
		t.Fatal("Post error = nil, want error")
	}
	if got := err.Error(); !strings.Contains(got, "channel_not_found") {
		t.Errorf("error = %q, want it to mention channel_not_found", got)
	}
}

func TestWebhookURLWinsOverBotToken(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c, err := New(Options{
		WebhookURL: srv.URL,
		BotToken:   "xoxb-token",
		Channel:    "#releases",
		APIBaseURL: "http://127.0.0.1:1/never",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Post(context.Background(), &Message{Text: "hi"}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if !hit {
		t.Error("Incoming Webhook was not used")
	}
}
