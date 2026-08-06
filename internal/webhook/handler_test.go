package webhook

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
)

const testSecret = "This is my secret"

// mockNotifier records the payloads it receives and can be made to fail.
type mockNotifier struct {
	calls []*Payload
	err   error
}

func (m *mockNotifier) Notify(_ context.Context, p *Payload) error {
	m.calls = append(m.calls, p)
	return m.err
}

func newTestHandler(t *testing.T, notifier Notifier, notifyPing bool) *Handler {
	t.Helper()
	h, err := NewHandler(Options{
		Secret:     testSecret,
		Path:       "/webhook",
		Notifier:   notifier,
		NotifyPing: notifyPing,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func signedRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set(SignatureHeader, "hmacsha256="+ComputeSignature(testSecret, []byte(body)))
	return req
}

func TestNewHandlerValidation(t *testing.T) {
	if _, err := NewHandler(Options{Notifier: &mockNotifier{}}); err == nil {
		t.Error("NewHandler without secret: error = nil, want error")
	}
	if _, err := NewHandler(Options{Secret: testSecret}); err == nil {
		t.Error("NewHandler without notifier: error = nil, want error")
	}
}

func TestHandlerAcceptsValidNotification(t *testing.T) {
	notifier := &mockNotifier{}
	h := newTestHandler(t, notifier, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(sampleNotification))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(notifier.calls))
	}
	if got := notifier.calls[0].Data.Type; got != "appStoreVersionAppVersionStateUpdated" {
		t.Errorf("notified type = %q", got)
	}
}

func TestHandlerRejectsBadSignature(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "missing", header: ""},
		{name: "wrong digest", header: "hmacsha256=" + strings.Repeat("0", 64)},
		{name: "bad prefix", header: "sha256=" + ComputeSignature(testSecret, []byte(sampleNotification))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier := &mockNotifier{}
			h := newTestHandler(t, notifier, true)

			req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(sampleNotification))
			if tt.header != "" {
				req.Header.Set(SignatureHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if body := rec.Body.String(); body != "" {
				t.Errorf("body = %q, want empty (no reason disclosed)", body)
			}
			if len(notifier.calls) != 0 {
				t.Errorf("notifier called %d times, want 0", len(notifier.calls))
			}
		})
	}
}

func TestHandlerRejectsInvalidJSON(t *testing.T) {
	notifier := &mockNotifier{}
	h := newTestHandler(t, notifier, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(`{"data":`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if len(notifier.calls) != 0 {
		t.Errorf("notifier called %d times, want 0", len(notifier.calls))
	}
}

func TestHandlerRejectsNonPOST(t *testing.T) {
	h := newTestHandler(t, &mockNotifier{}, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhook", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want %q", got, http.MethodPost)
	}
}

func TestHandlerUnknownPath(t *testing.T) {
	h := newTestHandler(t, &mockNotifier{}, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/nope", strings.NewReader("{}")))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandlerHealthz(t *testing.T) {
	h := newTestHandler(t, &mockNotifier{}, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, HealthPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestHandlerReturns502WhenSlackFails(t *testing.T) {
	notifier := &mockNotifier{err: errors.New("slack is down")}
	h := newTestHandler(t, notifier, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(sampleNotification))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestHandlerPing(t *testing.T) {
	const pingBody = `{"data":{"type":"webhookPing","id":"9d0d9f16-0000-0000-0000-000000000000"}}`

	tests := []struct {
		name       string
		notifyPing bool
		wantCalls  int
	}{
		{name: "notifies when enabled", notifyPing: true, wantCalls: 1},
		{name: "suppressed when disabled", notifyPing: false, wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier := &mockNotifier{}
			h := newTestHandler(t, notifier, tt.notifyPing)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, signedRequest(pingBody))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if len(notifier.calls) != tt.wantCalls {
				t.Fatalf("notifier called %d times, want %d", len(notifier.calls), tt.wantCalls)
			}
		})
	}
}

func TestHandlerNotifiesUnknownEventType(t *testing.T) {
	notifier := &mockNotifier{}
	h := newTestHandler(t, notifier, true)

	body := `{"data":{"type":"somethingBrandNewCreated","id":"1","attributes":{"name":"x"}}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(notifier.calls))
	}
}

func TestHandlerRejectsOversizedBody(t *testing.T) {
	notifier := &mockNotifier{}
	h := newTestHandler(t, notifier, true)

	body := strings.Repeat("a", MaxBodyBytes+1)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(body))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if len(notifier.calls) != 0 {
		t.Errorf("notifier called %d times, want 0", len(notifier.calls))
	}
}

func TestHandlerCustomPath(t *testing.T) {
	notifier := &mockNotifier{}
	h, err := NewHandler(Options{
		Secret:   testSecret,
		Path:     "/asc",
		Notifier: notifier,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/asc", strings.NewReader(sampleNotification))
	req.Header.Set(SignatureHeader, "hmacsha256="+ComputeSignature(testSecret, []byte(sampleNotification)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestHandlerThroughLambdaProxyWithBase64Body ensures the signature is verified
// against the decoded raw bytes when API Gateway base64-encodes the body.
func TestHandlerThroughLambdaProxyWithBase64Body(t *testing.T) {
	notifier := &mockNotifier{}
	h := newTestHandler(t, notifier, true)
	adapter := httpadapter.NewV2(h)

	req := events.APIGatewayV2HTTPRequest{
		RawPath: "/webhook",
		Headers: map[string]string{
			"content-type":       "application/json",
			"X-Apple-Signature":  "hmacsha256=" + ComputeSignature(testSecret, []byte(sampleNotification)),
			"content-length":     "",
			"x-forwarded-for":    "203.0.113.1",
			"x-forwarded-proto":  "https",
			"x-amzn-trace-id":    "Root=1-00000000-000000000000000000000000",
			"user-agent":         "AppStoreConnect/1.0",
			"accept":             "*/*",
			"x-forwarded-port":   "443",
			"x-amz-content-sha2": "",
		},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: http.MethodPost,
				Path:   "/webhook",
			},
		},
		Body:            base64.StdEncoding.EncodeToString([]byte(sampleNotification)),
		IsBase64Encoded: true,
	}

	resp, err := adapter.ProxyWithContext(context.Background(), req)
	if err != nil {
		t.Fatalf("ProxyWithContext: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", resp.StatusCode, http.StatusOK, resp.Body)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(notifier.calls))
	}
}
