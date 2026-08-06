package webhook

import (
	"reflect"
	"testing"
)

const sampleNotification = `{
  "data": {
    "type": "appStoreVersionAppVersionStateUpdated",
    "id": "7c813492-9516-4c79-903e-224effdd57ac",
    "version": 1,
    "attributes": {
      "newValue": "READY_FOR_REVIEW",
      "oldValue": "PREPARE_FOR_SUBMISSION",
      "timestamp": "2025-04-16T05:00:52.745Z"
    },
    "relationships": {
      "instance": {
        "data": { "type": "appStoreVersions", "id": "ad7e6298-0000-0000-0000-000000000000" }
      }
    }
  }
}`

func TestParseNotification(t *testing.T) {
	p, err := Parse([]byte(sampleNotification))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if p.Data.Type != "appStoreVersionAppVersionStateUpdated" {
		t.Errorf("Data.Type = %q", p.Data.Type)
	}
	if p.Data.ID != "7c813492-9516-4c79-903e-224effdd57ac" {
		t.Errorf("Data.ID = %q", p.Data.ID)
	}
	if p.Data.Version != 1 {
		t.Errorf("Data.Version = %d", p.Data.Version)
	}
	if got := p.Data.Attributes.OldValue(); got != "PREPARE_FOR_SUBMISSION" {
		t.Errorf("OldValue = %q", got)
	}
	if got := p.Data.Attributes.NewValue(); got != "READY_FOR_REVIEW" {
		t.Errorf("NewValue = %q", got)
	}
	if got := p.Data.Attributes.Timestamp(); got != "2025-04-16T05:00:52.745Z" {
		t.Errorf("Timestamp = %q", got)
	}
	if !p.Data.IsStateUpdate() {
		t.Error("IsStateUpdate = false, want true")
	}
	if p.IsPing() {
		t.Error("IsPing = true, want false")
	}

	inst := p.Data.Instance()
	if inst == nil {
		t.Fatal("Instance = nil")
	}
	if inst.Type != "appStoreVersions" || inst.ID != "ad7e6298-0000-0000-0000-000000000000" {
		t.Errorf("Instance = %+v", inst)
	}
	if !isKnownEventType(p.Data.Type) {
		t.Error("isKnownEventType = false, want true")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{"data":`},
		{name: "missing type", body: `{"data":{"id":"abc"}}`},
		{name: "empty body", body: ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.body)); err == nil {
				t.Fatal("Parse error = nil, want error")
			}
		})
	}
}

func TestIsPing(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "ping data type", body: `{"data":{"type":"webhookPing","id":"1"}}`, want: true},
		{name: "ping flag", body: `{"ping":true,"data":{"type":"buildUploadStateUpdated","id":"1"}}`, want: true},
		{name: "ping attribute", body: `{"data":{"type":"buildUploadStateUpdated","id":"1","attributes":{"ping":true}}}`, want: true},
		{name: "ping flag false", body: `{"ping":false,"data":{"type":"buildUploadStateUpdated","id":"1"}}`, want: false},
		{name: "regular event", body: sampleNotification, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse([]byte(tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := p.IsPing(); got != tt.want {
				t.Fatalf("IsPing = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnknownEventTypeIsParsedGenerically(t *testing.T) {
	body := `{"data":{"type":"somethingBrandNewCreated","id":"1","attributes":{"count":3,"name":"x"}}}`
	p, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if isKnownEventType(p.Data.Type) {
		t.Error("isKnownEventType = true, want false")
	}
	if p.Data.IsStateUpdate() {
		t.Error("IsStateUpdate = true, want false")
	}
	if got := p.Data.Attributes.String("count"); got != "3" {
		t.Errorf("String(count) = %q, want \"3\"", got)
	}
	if got := p.Data.Attributes.SortedKeys(); !reflect.DeepEqual(got, []string{"count", "name"}) {
		t.Errorf("SortedKeys = %v", got)
	}
	if p.Data.Instance() != nil {
		t.Error("Instance != nil, want nil")
	}
}
