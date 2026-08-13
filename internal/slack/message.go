package slack

import (
	"fmt"
	"strings"
	"unicode"

	"go.ngs.io/asc-slack-notifier/internal/webhook"
)

// Message is a Slack message payload. The same shape works for both Incoming
// Webhooks and chat.postMessage; Channel is only set for the latter.
type Message struct {
	Channel string  `json:"channel,omitempty"`
	Text    string  `json:"text"`
	Blocks  []Block `json:"blocks,omitempty"`
}

// Block is a Block Kit block. Only the subset used by this service is modeled.
// Elements holds Text objects in a context block and Button objects in an
// actions block; the model is marshal-only, so a single slice of any suffices.
type Block struct {
	Type     string `json:"type"`
	Text     *Text  `json:"text,omitempty"`
	Fields   []Text `json:"fields,omitempty"`
	Elements []any  `json:"elements,omitempty"`
}

// Text is a Block Kit composition object.
type Text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Button is a Block Kit button element that opens a URL.
type Button struct {
	Type string `json:"type"`
	Text Text   `json:"text"`
	URL  string `json:"url"`
}

// Enrichment carries data fetched from the App Store Connect API that is not
// present in the webhook payload itself. A nil Enrichment renders exactly the
// message the payload alone produces.
type Enrichment struct {
	AppID         string
	AppName       string
	VersionString string
	BuildNumber   string
	Platform      string
}

const (
	blockHeader  = "header"
	blockSection = "section"
	blockContext = "context"
	blockActions = "actions"

	elementButton = "button"

	textPlain  = "plain_text"
	textMrkdwn = "mrkdwn"
)

// appStoreConnectAppURL is the App Store Connect distribution page of an app.
const appStoreConnectAppURL = "https://appstoreconnect.apple.com/apps/%s/distribution"

// stateEmoji maps App Store Connect state values to a leading emoji. Values not
// listed fall back to the heuristics in emojiForState.
var stateEmoji = map[string]string{
	"PREPARE_FOR_SUBMISSION":      "✏️",
	"READY_FOR_REVIEW":            "📝",
	"WAITING_FOR_REVIEW":          "⏳",
	"IN_REVIEW":                   "🔍",
	"PENDING_DEVELOPER_RELEASE":   "⏳",
	"PENDING_APPLE_RELEASE":       "⏳",
	"PROCESSING_FOR_DISTRIBUTION": "⚙️",
	"READY_FOR_DISTRIBUTION":      "✅",
	"ACCEPTED":                    "✅",
	"APPROVED":                    "✅",
	"COMPLETE":                    "✅",
	"COMPLETED":                   "✅",
	"REJECTED":                    "❌",
	"DEVELOPER_REJECTED":          "❌",
	"METADATA_REJECTED":           "❌",
	"INVALID_BINARY":              "❌",
	"FAILED":                      "❌",
	"REPLACED_WITH_NEW_VERSION":   "🔄",
	"DEVELOPER_REMOVED_FROM_SALE": "🚫",
	"REMOVED_FROM_SALE":           "🚫",
	"NOT_APPLICABLE":              "ℹ️",
	"PROCESSING":                  "⚙️",
	"UPLOADED":                    "📦",
}

// emojiSubstrings is consulted in order when a state is not in stateEmoji.
var emojiSubstrings = []struct {
	needle string
	emoji  string
}{
	{"REJECT", "❌"},
	{"FAIL", "❌"},
	{"INVALID", "❌"},
	{"ERROR", "❌"},
	{"EXPIRED", "❌"},
	{"PENDING", "⏳"},
	{"WAITING", "⏳"},
	{"PROCESSING", "⚙️"},
	{"READY", "📝"},
	{"AVAILABLE", "✅"},
}

const defaultEmoji = "ℹ️"

// emojiForState returns the emoji prefix for a state value.
func emojiForState(state string) string {
	if state == "" {
		return defaultEmoji
	}
	upper := strings.ToUpper(state)
	if e, ok := stateEmoji[upper]; ok {
		return e
	}
	for _, rule := range emojiSubstrings {
		if strings.Contains(upper, rule.needle) {
			return rule.emoji
		}
	}
	return defaultEmoji
}

// BuildMessage renders a webhook payload as a Block Kit message. Three shapes
// are produced: a ping acknowledgement, a state transition, and a generic
// key/value rendering used for creation and unknown event types. When e is
// non-nil, the App Store Connect data it carries is folded into the fields, the
// fallback text and an "Open in App Store Connect" button.
func BuildMessage(p *webhook.Payload, e *Enrichment) *Message {
	if p.IsPing() {
		return buildPingMessage(p)
	}

	title := Humanize(p.Data.Type)
	emoji := emojiForState(p.Data.Attributes.NewValue())
	header := fmt.Sprintf("%s %s", emoji, title)

	msg := &Message{
		Text: header,
		Blocks: []Block{
			{Type: blockHeader, Text: &Text{Type: textPlain, Text: truncate(header, 150)}},
		},
	}

	fields := buildFields(p, e)
	if len(fields) > 0 {
		msg.Blocks = append(msg.Blocks, Block{Type: blockSection, Fields: fields})
	}

	if e != nil && e.AppID != "" {
		msg.Blocks = append(msg.Blocks, Block{Type: blockActions, Elements: []any{Button{
			Type: elementButton,
			Text: Text{Type: textPlain, Text: "Open in App Store Connect"},
			URL:  fmt.Sprintf(appStoreConnectAppURL, e.AppID),
		}}})
	}

	if ctx := contextLine(p); ctx != "" {
		msg.Blocks = append(msg.Blocks, Block{
			Type:     blockContext,
			Elements: []any{Text{Type: textMrkdwn, Text: ctx}},
		})
	}

	if p.Data.IsStateUpdate() {
		msg.Text = fmt.Sprintf("%s%s: %s → %s", header, subjectSuffix(e),
			orDash(p.Data.Attributes.OldValue()), orDash(p.Data.Attributes.NewValue()))
	}
	return msg
}

// subjectSuffix names the app and version the event is about, for the fallback
// text shown in notifications and channel lists.
func subjectSuffix(e *Enrichment) string {
	if e == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if e.AppName != "" {
		parts = append(parts, e.AppName)
	}
	if e.VersionString != "" {
		parts = append(parts, e.VersionString)
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, " ")
}

func buildPingMessage(p *webhook.Payload) *Message {
	text := "🏓 App Store Connect webhook ping received"
	return &Message{
		Text: text,
		Blocks: []Block{
			{Type: blockSection, Text: &Text{Type: textMrkdwn, Text: text}},
			{Type: blockContext, Elements: []any{Text{
				Type: textMrkdwn,
				Text: fmt.Sprintf("type: `%s`", p.Data.Type),
			}}},
		},
	}
}

// buildFields renders the event attributes as Block Kit fields. Enrichment
// data, when present, leads. State updates get a dedicated transition field;
// every other attribute is rendered as a key/value pair in a stable order.
func buildFields(p *webhook.Payload, e *Enrichment) []Text {
	attrs := p.Data.Attributes
	var fields []Text

	if e != nil {
		for _, f := range []struct{ label, value string }{
			{"App", e.AppName},
			{"Version", e.VersionString},
			{"Build", e.BuildNumber},
		} {
			if f.value == "" {
				continue
			}
			fields = append(fields, Text{
				Type: textMrkdwn,
				Text: fmt.Sprintf("*%s*\n%s", f.label, truncate(f.value, 200)),
			})
		}
	}

	if p.Data.IsStateUpdate() {
		fields = append(fields, Text{
			Type: textMrkdwn,
			Text: fmt.Sprintf("*Status*\n`%s` → `%s`",
				orDash(attrs.OldValue()), orDash(attrs.NewValue())),
		})
	}

	skip := map[string]bool{"timestamp": true}
	if p.Data.IsStateUpdate() {
		skip["oldValue"] = true
		skip["newValue"] = true
	}
	for _, k := range attrs.SortedKeys() {
		if skip[k] {
			continue
		}
		v := attrs.String(k)
		if v == "" {
			continue
		}
		fields = append(fields, Text{
			Type: textMrkdwn,
			Text: fmt.Sprintf("*%s*\n%s", Humanize(k), truncate(v, 200)),
		})
	}

	if ts := attrs.Timestamp(); ts != "" {
		fields = append(fields, Text{
			Type: textMrkdwn,
			Text: fmt.Sprintf("*Timestamp*\n%s", ts),
		})
	}
	// The raw instance UUID is only worth showing when nothing better is known:
	// an enriched message names the app and version instead.
	enriched := e != nil && e.VersionString != ""
	if inst := p.Data.Instance(); !enriched && inst != nil && (inst.Type != "" || inst.ID != "") {
		fields = append(fields, Text{
			Type: textMrkdwn,
			Text: fmt.Sprintf("*%s*\n`%s`", Humanize(orValue(inst.Type, "instance")), inst.ID),
		})
	}

	// Slack renders at most 10 fields per section.
	if len(fields) > 10 {
		fields = fields[:10]
	}
	return fields
}

func contextLine(p *webhook.Payload) string {
	var parts []string
	if p.Data.Type != "" {
		parts = append(parts, fmt.Sprintf("type: `%s`", p.Data.Type))
	}
	if p.Data.ID != "" {
		parts = append(parts, fmt.Sprintf("event: `%s`", p.Data.ID))
	}
	return strings.Join(parts, "  |  ")
}

// Humanize converts a lowerCamelCase identifier into a spaced, title-cased
// label, e.g. appStoreVersionAppVersionStateUpdated becomes
// "App Store Version App Version State Updated".
func Humanize(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(runes[i-1]) {
			b.WriteRune(' ')
		}
		if i == 0 {
			r = unicode.ToUpper(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func orDash(s string) string { return orValue(s, "—") }

func orValue(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
