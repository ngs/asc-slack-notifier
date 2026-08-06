// Package webhook implements signature verification, payload parsing and the
// HTTP handler for App Store Connect webhook notifications.
package webhook

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Payload is the top level body App Store Connect posts to the webhook URL.
type Payload struct {
	Data Data `json:"data"`
	// Ping is the WebhookEvent flag marking a test delivery. It is a pointer so
	// that an absent flag is distinguishable from an explicit false.
	Ping *bool `json:"ping,omitempty"`
}

// Data carries the event itself.
type Data struct {
	Type          string         `json:"type"`
	ID            string         `json:"id"`
	Version       int            `json:"version,omitempty"`
	Attributes    Attributes     `json:"attributes,omitempty"`
	Relationships *Relationships `json:"relationships,omitempty"`
}

// Attributes is the free-form attribute bag of an event. It is kept generic so
// that event types unknown to this service still render usefully.
type Attributes map[string]any

// Relationships holds the resource an event refers to.
type Relationships struct {
	Instance *Relationship `json:"instance,omitempty"`
}

// Relationship is a JSON:API style relationship object.
type Relationship struct {
	Data *ResourceIdentifier `json:"data,omitempty"`
}

// ResourceIdentifier identifies an App Store Connect resource.
type ResourceIdentifier struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// KnownEventTypes lists the data.type values documented by App Store Connect,
// in the lowerCamelCase form used in webhook payloads. Types outside this list
// are still delivered, using the generic formatting.
var KnownEventTypes = []string{
	"alternativeDistributionPackageAvailableUpdated",
	"alternativeDistributionPackageVersionCreated",
	"alternativeDistributionTerritoryAvailabilityUpdated",
	"appStoreVersionAppVersionStateUpdated",
	"backgroundAssetVersionAppStoreReleaseStateUpdated",
	"backgroundAssetVersionExternalBetaReleaseStateUpdated",
	"backgroundAssetVersionInternalBetaReleaseCreated",
	"backgroundAssetVersionStateUpdated",
	"betaFeedbackCrashSubmissionCreated",
	"betaFeedbackScreenshotSubmissionCreated",
	"buildBetaDetailExternalBuildStateUpdated",
	"buildUploadStateUpdated",
}

var knownEventTypes = func() map[string]bool {
	m := make(map[string]bool, len(KnownEventTypes))
	for _, t := range KnownEventTypes {
		m[t] = true
	}
	return m
}()

// isKnownEventType reports whether the type is one Apple documents.
func isKnownEventType(t string) bool { return knownEventTypes[t] }

// Parse decodes a raw webhook body. Unknown fields are tolerated so that new
// event types introduced by Apple never cause a delivery to be dropped.
func Parse(body []byte) (*Payload, error) {
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse webhook payload: %w", err)
	}
	if p.Data.Type == "" {
		return nil, fmt.Errorf("parse webhook payload: missing data.type")
	}
	return &p, nil
}

// IsPing reports whether the payload is a test delivery rather than a real
// event, using either the WebhookEvent flag or the ping event type.
func (p *Payload) IsPing() bool {
	if p == nil {
		return false
	}
	if p.Ping != nil && *p.Ping {
		return true
	}
	if b, ok := p.Data.Attributes["ping"].(bool); ok && b {
		return true
	}
	return strings.Contains(strings.ToLower(p.Data.Type), "ping")
}

// OldValue returns the previous state of a state-update event.
func (a Attributes) OldValue() string { return a.String("oldValue") }

// NewValue returns the new state of a state-update event.
func (a Attributes) NewValue() string { return a.String("newValue") }

// Timestamp returns the event timestamp as sent by App Store Connect.
func (a Attributes) Timestamp() string { return a.String("timestamp") }

// String returns the attribute rendered as a string, or an empty string when
// the key is absent.
func (a Attributes) String(key string) string {
	v, ok := a[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// SortedKeys returns the attribute keys in a stable order so that rendered
// messages do not vary between deliveries.
func (a Attributes) SortedKeys() []string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IsStateUpdate reports whether the event carries an old/new value pair.
func (d Data) IsStateUpdate() bool {
	return d.Attributes.OldValue() != "" || d.Attributes.NewValue() != ""
}

// Instance returns the related resource identifier, if any.
func (d Data) Instance() *ResourceIdentifier {
	if d.Relationships == nil || d.Relationships.Instance == nil {
		return nil
	}
	return d.Relationships.Instance.Data
}
