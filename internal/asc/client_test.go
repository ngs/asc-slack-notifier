package asc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const versionFixture = `{
  "data": {
    "type": "appStoreVersions",
    "id": "ad7e6298-0000-0000-0000-000000000000",
    "attributes": {"versionString": "2.3.1", "platform": "IOS"},
    "relationships": {
      "app": {"data": {"type": "apps", "id": "1234567890"}},
      "build": {"data": {"type": "builds", "id": "b1"}}
    }
  },
  "included": [
    {"type": "apps", "id": "1234567890", "attributes": {"name": "MyApp", "bundleId": "io.ngs.MyApp"}},
    {"type": "builds", "id": "b1", "attributes": {"version": "123"}}
  ]
}`

const versionFixtureNoBuild = `{
  "data": {
    "type": "appStoreVersions",
    "id": "ad7e6298-0000-0000-0000-000000000000",
    "attributes": {"versionString": "2.3.1", "platform": "IOS"},
    "relationships": {
      "app": {"data": {"type": "apps", "id": "1234567890"}},
      "build": {"data": null}
    }
  },
  "included": [
    {"type": "apps", "id": "1234567890", "attributes": {"name": "MyApp", "bundleId": "io.ngs.MyApp"}}
  ]
}`

// newTestClient builds a Client signing with a throwaway key and talking to srv.
func newTestClient(t *testing.T, baseURL string, httpClient *http.Client) *Client {
	t.Helper()
	keyPEM, _ := testKeyPEM(t)
	c, err := New(Options{
		KeyID:         "KEYID123",
		IssuerID:      "issuer-uuid",
		PrivateKeyPEM: keyPEM,
		BaseURL:       baseURL,
		HTTPClient:    httpClient,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestAppStoreVersionInfo(t *testing.T) {
	var gotPath, gotAuth string
	var gotQuery map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotQuery = map[string]string{}
		for k, v := range r.URL.Query() {
			gotQuery[k] = v[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, versionFixture)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, srv.Client())
	info, err := c.AppStoreVersionInfo(context.Background(), "ad7e6298")
	if err != nil {
		t.Fatalf("AppStoreVersionInfo: %v", err)
	}

	if gotPath != "/v1/appStoreVersions/ad7e6298" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") || len(gotAuth) <= len("Bearer ") {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}
	wantQuery := map[string]string{
		"include":                  "app,build",
		"fields[appStoreVersions]": "versionString,platform",
		"fields[apps]":             "name,bundleId",
		"fields[builds]":           "version",
	}
	for k, want := range wantQuery {
		if gotQuery[k] != want {
			t.Errorf("query %s = %q, want %q", k, gotQuery[k], want)
		}
	}

	want := VersionInfo{
		AppID:         "1234567890",
		AppName:       "MyApp",
		BundleID:      "io.ngs.MyApp",
		VersionString: "2.3.1",
		BuildNumber:   "123",
		Platform:      "IOS",
	}
	if *info != want {
		t.Errorf("info = %+v, want %+v", *info, want)
	}
}

func TestAppStoreVersionInfoWithoutBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, versionFixtureNoBuild)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, srv.Client())
	info, err := c.AppStoreVersionInfo(context.Background(), "ad7e6298")
	if err != nil {
		t.Fatalf("AppStoreVersionInfo: %v", err)
	}
	if info.BuildNumber != "" {
		t.Errorf("BuildNumber = %q, want empty when no build is attached", info.BuildNumber)
	}
	if info.AppName != "MyApp" || info.VersionString != "2.3.1" {
		t.Errorf("info = %+v", *info)
	}
}

func TestAppStoreVersionInfoErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"errors":[{"status":"404"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, srv.Client())
	_, err := c.AppStoreVersionInfo(context.Background(), "missing")
	if err == nil {
		t.Fatal("error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want it to mention the status code", err)
	}
}

func TestAppStoreVersionInfoRequiresID(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:1", nil)
	if _, err := c.AppStoreVersionInfo(context.Background(), ""); err == nil {
		t.Fatal("error = nil, want an error for an empty version ID")
	}
}

// buildFixture mirrors a real /v1/builds response: the app arrives through
// `included` even though the relationship itself carries no data.
const buildFixture = `{
  "data": {
    "type": "builds",
    "id": "e59c1dca-0af1-4df1-9cb4-054d62c569e5",
    "attributes": {"version": "31661572690"},
    "relationships": {"app": {"data": null}}
  },
  "included": [
    {"type": "apps", "id": "6757988472", "attributes": {"name": "MyApp", "bundleId": "io.ngs.MyApp"}},
    {"type": "preReleaseVersions", "id": "pr1", "attributes": {"version": "1.1.0", "platform": "IOS"}}
  ]
}`

const buildFixtureNoPreReleaseVersion = `{
  "data": {
    "type": "builds",
    "id": "e59c1dca-0af1-4df1-9cb4-054d62c569e5",
    "attributes": {"version": "31661572690"},
    "relationships": {"app": {"data": {"type": "apps", "id": "6757988472"}}}
  },
  "included": [
    {"type": "apps", "id": "6757988472", "attributes": {"name": "MyApp", "bundleId": "io.ngs.MyApp"}}
  ]
}`

func TestBuildInfo(t *testing.T) {
	var gotPath string
	var gotQuery map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = map[string]string{}
		for k, v := range r.URL.Query() {
			gotQuery[k] = v[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, buildFixture)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, srv.Client())
	info, err := c.BuildInfo(context.Background(), "e59c1dca")
	if err != nil {
		t.Fatalf("BuildInfo: %v", err)
	}

	if gotPath != "/v1/builds/e59c1dca" {
		t.Errorf("path = %q", gotPath)
	}
	wantQuery := map[string]string{
		"include":                    "app,preReleaseVersion",
		"fields[builds]":             "version",
		"fields[apps]":               "name,bundleId",
		"fields[preReleaseVersions]": "version,platform",
	}
	for k, want := range wantQuery {
		if gotQuery[k] != want {
			t.Errorf("query %s = %q, want %q", k, gotQuery[k], want)
		}
	}

	want := VersionInfo{
		AppID:         "6757988472",
		AppName:       "MyApp",
		BundleID:      "io.ngs.MyApp",
		VersionString: "1.1.0",
		BuildNumber:   "31661572690",
		Platform:      "IOS",
	}
	if *info != want {
		t.Errorf("info = %+v, want %+v", *info, want)
	}
}

func TestBuildInfoWithoutPreReleaseVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, buildFixtureNoPreReleaseVersion)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, srv.Client())
	info, err := c.BuildInfo(context.Background(), "e59c1dca")
	if err != nil {
		t.Fatalf("BuildInfo: %v", err)
	}
	if info.VersionString != "" || info.Platform != "" {
		t.Errorf("info = %+v, want no version when the pre-release version is absent", *info)
	}
	if info.AppID != "6757988472" || info.BuildNumber != "31661572690" {
		t.Errorf("info = %+v", *info)
	}
}

func TestBuildInfoRequiresID(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:1", nil)
	if _, err := c.BuildInfo(context.Background(), ""); err == nil {
		t.Fatal("error = nil, want an error for an empty build ID")
	}
}
