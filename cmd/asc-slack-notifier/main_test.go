package main

import "testing"

func TestTestFlightURL(t *testing.T) {
	tests := []struct {
		name     string
		appID    string
		platform string
		want     string
	}{
		{
			name:     "ios",
			appID:    "6757988472",
			platform: "IOS",
			want:     "https://appstoreconnect.apple.com/apps/6757988472/testflight/ios",
		},
		{
			name:     "vision os",
			appID:    "6757988472",
			platform: "VISION_OS",
			want:     "https://appstoreconnect.apple.com/apps/6757988472/testflight/visionos",
		},
		{
			name:     "unknown platform drops the segment",
			appID:    "6757988472",
			platform: "WATCH_OS",
			want:     "https://appstoreconnect.apple.com/apps/6757988472/testflight",
		},
		{
			name:     "empty platform drops the segment",
			appID:    "6757988472",
			platform: "",
			want:     "https://appstoreconnect.apple.com/apps/6757988472/testflight",
		},
		{
			name:     "no app id",
			appID:    "",
			platform: "IOS",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testFlightURL(tt.appID, tt.platform); got != tt.want {
				t.Errorf("testFlightURL(%q, %q) = %q, want %q", tt.appID, tt.platform, got, tt.want)
			}
		})
	}
}
