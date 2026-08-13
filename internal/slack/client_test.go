package slack

import (
	"context"
	"encoding/json"
	"errors"
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

// fakeEnricher records the version ID it was asked about and returns a canned
// result, or an error when one is set.
type fakeEnricher struct {
	calls  []string
	result *Enrichment
	err    error
}

func (f *fakeEnricher) EnrichAppStoreVersion(_ context.Context, versionID string) (*Enrichment, error) {
	f.calls = append(f.calls, versionID)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// notifyWithEnricher posts body through a Client wired to enricher and returns
// the raw JSON Slack received.
func notifyWithEnricher(t *testing.T, enricher Enricher, body string) string {
	t.Helper()
	var posted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c, err := New(Options{WebhookURL: srv.URL, HTTPClient: srv.Client(), Enricher: enricher})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p, err := webhook.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := c.Notify(context.Background(), p); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	return string(posted)
}

const appStoreVersionEvent = `{"data":{"type":"appStoreVersionAppVersionStateUpdated","id":"1",
  "attributes":{"oldValue":"IN_REVIEW","newValue":"READY_FOR_DISTRIBUTION"},
  "relationships":{"instance":{"data":{"type":"appStoreVersions","id":"ad7e6298"}}}}}`

func TestNotifyEnrichesAppStoreVersions(t *testing.T) {
	enricher := &fakeEnricher{result: &Enrichment{
		AppID: "1234567890", AppName: "MyApp", VersionString: "2.3.1", BuildNumber: "123",
	}}

	sent := notifyWithEnricher(t, enricher, appStoreVersionEvent)

	if len(enricher.calls) != 1 || enricher.calls[0] != "ad7e6298" {
		t.Fatalf("enricher calls = %v, want one call for ad7e6298", enricher.calls)
	}
	for _, want := range []string{
		"*App*", "MyApp", "2.3.1",
		"https://appstoreconnect.apple.com/apps/1234567890/distribution",
	} {
		if !strings.Contains(sent, want) {
			t.Errorf("posted message missing %q:\n%s", want, sent)
		}
	}
}

func TestNotifyContinuesWhenEnrichmentFails(t *testing.T) {
	enricher := &fakeEnricher{err: errors.New("app store connect is down")}

	sent := notifyWithEnricher(t, enricher, appStoreVersionEvent)

	if len(enricher.calls) != 1 {
		t.Fatalf("enricher calls = %v, want one", enricher.calls)
	}
	if !strings.Contains(sent, "READY_FOR_DISTRIBUTION") {
		t.Errorf("message was not posted un-enriched:\n%s", sent)
	}
	if strings.Contains(sent, "appstoreconnect.apple.com") {
		t.Errorf("failed enrichment still produced a button:\n%s", sent)
	}
}

func TestNotifySkipsEnrichmentForOtherResources(t *testing.T) {
	enricher := &fakeEnricher{result: &Enrichment{AppID: "1234567890", AppName: "MyApp"}}

	body := `{"data":{"type":"buildUploadStateUpdated","id":"1",
	  "attributes":{"oldValue":"PROCESSING","newValue":"COMPLETE"},
	  "relationships":{"instance":{"data":{"type":"builds","id":"b1"}}}}}`
	sent := notifyWithEnricher(t, enricher, body)

	if len(enricher.calls) != 0 {
		t.Errorf("enricher called for a non-appStoreVersions instance: %v", enricher.calls)
	}
	if strings.Contains(sent, "MyApp") {
		t.Errorf("message carries enrichment data it should not have:\n%s", sent)
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
