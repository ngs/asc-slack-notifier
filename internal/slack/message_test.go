package slack

import (
	"encoding/json"
	"strings"
	"testing"

	"go.ngs.io/asc-slack-notifier/internal/webhook"
)

func mustParse(t *testing.T, body string) *webhook.Payload {
	t.Helper()
	p, err := webhook.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestHumanize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "appStoreVersionAppVersionStateUpdated", want: "App Store Version App Version State Updated"},
		{in: "buildUploadStateUpdated", want: "Build Upload State Updated"},
		{in: "newValue", want: "New Value"},
		{in: "timestamp", want: "Timestamp"},
		{in: "", want: ""},
	}
	for _, tt := range tests {
		if got := Humanize(tt.in); got != tt.want {
			t.Errorf("Humanize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEmojiForState(t *testing.T) {
	tests := []struct {
		state string
		want  string
	}{
		{state: "READY_FOR_REVIEW", want: "📝"},
		{state: "PENDING_APPLE_RELEASE", want: "⏳"},
		{state: "READY_FOR_DISTRIBUTION", want: "✅"},
		{state: "ACCEPTED", want: "✅"},
		{state: "REJECTED", want: "❌"},
		{state: "METADATA_REJECTED", want: "❌"},
		{state: "PROCESSING_FAILED", want: "❌"},
		{state: "SOMETHING_ENTIRELY_NEW", want: defaultEmoji},
		{state: "", want: defaultEmoji},
	}
	for _, tt := range tests {
		if got := emojiForState(tt.state); got != tt.want {
			t.Errorf("emojiForState(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestBuildMessageStateUpdate(t *testing.T) {
	body := `{"data":{"type":"appStoreVersionAppVersionStateUpdated","id":"7c813492",
	  "attributes":{"newValue":"READY_FOR_REVIEW","oldValue":"PREPARE_FOR_SUBMISSION",
	  "timestamp":"2025-04-16T05:00:52.745Z"},
	  "relationships":{"instance":{"data":{"type":"appStoreVersions","id":"ad7e6298"}}}}}`

	msg := BuildMessage(mustParse(t, body))

	if len(msg.Blocks) == 0 || msg.Blocks[0].Type != blockHeader {
		t.Fatalf("first block = %+v, want a header block", msg.Blocks)
	}
	header := msg.Blocks[0].Text.Text
	if !strings.Contains(header, "App Store Version App Version State Updated") {
		t.Errorf("header = %q, missing humanized type", header)
	}
	if !strings.HasPrefix(header, "📝") {
		t.Errorf("header = %q, want the READY_FOR_REVIEW emoji prefix", header)
	}
	if !strings.Contains(msg.Text, "PREPARE_FOR_SUBMISSION") || !strings.Contains(msg.Text, "READY_FOR_REVIEW") {
		t.Errorf("fallback text = %q, want the state transition", msg.Text)
	}

	rendered := renderBlocks(msg)
	for _, want := range []string{
		"PREPARE_FOR_SUBMISSION", "READY_FOR_REVIEW",
		"2025-04-16T05:00:52.745Z", "ad7e6298", "App Store Versions",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered message missing %q:\n%s", want, rendered)
		}
	}
	// oldValue/newValue are rendered once, by the dedicated status field.
	if n := strings.Count(rendered, "READY_FOR_REVIEW"); n != 1 {
		t.Errorf("READY_FOR_REVIEW appears %d times, want 1:\n%s", n, rendered)
	}
}

func TestBuildMessageCreatedEvent(t *testing.T) {
	body := `{"data":{"type":"betaFeedbackCrashSubmissionCreated","id":"abc",
	  "attributes":{"timestamp":"2025-04-16T05:00:52.745Z"},
	  "relationships":{"instance":{"data":{"type":"betaFeedbackCrashSubmissions","id":"xyz"}}}}}`

	msg := BuildMessage(mustParse(t, body))

	if got := msg.Blocks[0].Text.Text; !strings.Contains(got, "Beta Feedback Crash Submission Created") {
		t.Errorf("header = %q", got)
	}
	if !strings.HasPrefix(msg.Blocks[0].Text.Text, defaultEmoji) {
		t.Errorf("header = %q, want the default emoji for a non-state event", msg.Blocks[0].Text.Text)
	}
	if rendered := renderBlocks(msg); !strings.Contains(rendered, "xyz") {
		t.Errorf("rendered message missing the instance id:\n%s", rendered)
	}
}

func TestBuildMessageUnknownType(t *testing.T) {
	body := `{"data":{"type":"somethingBrandNewCreated","id":"1",
	  "attributes":{"count":3,"name":"widget"}}}`

	msg := BuildMessage(mustParse(t, body))

	if got := msg.Blocks[0].Text.Text; !strings.Contains(got, "Something Brand New Created") {
		t.Errorf("header = %q", got)
	}
	rendered := renderBlocks(msg)
	for _, want := range []string{"Count", "3", "Name", "widget", "somethingBrandNewCreated"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered message missing %q:\n%s", want, rendered)
		}
	}
}

func TestBuildMessagePing(t *testing.T) {
	msg := BuildMessage(mustParse(t, `{"data":{"type":"webhookPing","id":"1"}}`))

	if !strings.Contains(msg.Text, "ping") {
		t.Errorf("text = %q, want a ping acknowledgement", msg.Text)
	}
	for _, b := range msg.Blocks {
		if b.Type == blockHeader {
			t.Errorf("ping message should not use a header block: %+v", b)
		}
	}
}

func TestBuildMessageIsValidJSON(t *testing.T) {
	msg := BuildMessage(mustParse(t, `{"data":{"type":"buildUploadStateUpdated","id":"1",
	  "attributes":{"oldValue":"PROCESSING","newValue":"COMPLETE"}}}`))

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !json.Valid(b) {
		t.Fatal("marshaled message is not valid JSON")
	}
	if strings.Contains(string(b), `"channel"`) {
		t.Error("message should not carry a channel until the client adds one")
	}
}

func TestBuildMessageCapsFieldCount(t *testing.T) {
	attrs := make(map[string]any, 20)
	for i := range 20 {
		attrs[string(rune('a'+i))] = i + 1
	}
	body, err := json.Marshal(map[string]any{
		"data": map[string]any{"type": "manyAttributesUpdated", "id": "1", "attributes": attrs},
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	msg := BuildMessage(mustParse(t, string(body)))
	for _, b := range msg.Blocks {
		if b.Type == blockSection && len(b.Fields) > 10 {
			t.Fatalf("section has %d fields, want at most 10", len(b.Fields))
		}
	}
}

// renderBlocks flattens every text fragment of a message for assertions.
func renderBlocks(msg *Message) string {
	var sb strings.Builder
	for _, b := range msg.Blocks {
		if b.Text != nil {
			sb.WriteString(b.Text.Text + "\n")
		}
		for _, f := range b.Fields {
			sb.WriteString(f.Text + "\n")
		}
		for _, e := range b.Elements {
			sb.WriteString(e.Text + "\n")
		}
	}
	return sb.String()
}
