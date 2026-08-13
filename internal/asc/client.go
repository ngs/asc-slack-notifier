// Package asc is a minimal App Store Connect API client. It fetches the
// supplemental data a webhook payload does not carry — app name, version string
// and build number — authenticating with an ES256 JWT signed by an App Store
// Connect API key.
package asc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// defaultBaseURL is the App Store Connect API root.
const defaultBaseURL = "https://api.appstoreconnect.apple.com"

// Options configures a Client.
type Options struct {
	// KeyID is the App Store Connect API key ID, used as the JWT `kid`.
	KeyID string
	// IssuerID is the App Store Connect API issuer ID, used as the JWT `iss`.
	IssuerID string
	// PrivateKeyPEM is the PEM encoded ECDSA private key of the API key.
	PrivateKeyPEM []byte
	// HTTPClient overrides the default client. Intended for tests.
	HTTPClient *http.Client
	// BaseURL overrides the API root. Intended for tests.
	BaseURL string
}

// VersionInfo is the enrichment data for an App Store version. Every field is
// best effort: the API omits pieces that do not exist yet, for example the
// build of a version no binary has been attached to.
type VersionInfo struct {
	AppID         string
	AppName       string
	BundleID      string
	VersionString string
	BuildNumber   string
	Platform      string
}

// Client queries the App Store Connect API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	tokens     *tokenSource
	now        func() time.Time
}

// New builds a Client from opts. The private key is parsed eagerly, so a
// malformed key fails at startup rather than on the first webhook delivery.
func New(opts Options) (*Client, error) {
	tokens, err := newTokenSource(opts.KeyID, opts.IssuerID, opts.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	c := &Client{
		baseURL:    opts.BaseURL,
		httpClient: opts.HTTPClient,
		tokens:     tokens,
		now:        time.Now,
	}
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return c, nil
}

// versionResponse models the subset of the JSON:API response this client reads.
type versionResponse struct {
	Data struct {
		Attributes struct {
			VersionString string `json:"versionString"`
			Platform      string `json:"platform"`
		} `json:"attributes"`
		Relationships struct {
			App struct {
				Data *struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"app"`
		} `json:"relationships"`
	} `json:"data"`
	Included []struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Name     string `json:"name"`
			BundleID string `json:"bundleId"`
			Version  string `json:"version"`
		} `json:"attributes"`
	} `json:"included"`
}

// get issues an authenticated GET against path and returns the response body.
// A non-2xx status is an error carrying the beginning of the body, which is
// where the App Store Connect API puts its error details.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	token, err := c.tokens.Token(c.now())
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s%s?%s", c.baseURL, path, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("asc: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asc: get %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("asc: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("asc: %s returned %d: %s",
			path, resp.StatusCode, bytes.TrimSpace(body[:min(len(body), 512)]))
	}
	return body, nil
}

// AppStoreVersionInfo fetches the app, version and build of an appStoreVersions
// resource.
func (c *Client) AppStoreVersionInfo(ctx context.Context, versionID string) (*VersionInfo, error) {
	if versionID == "" {
		return nil, fmt.Errorf("asc: version ID is required")
	}

	q := url.Values{}
	q.Set("include", "app,build")
	q.Set("fields[appStoreVersions]", "versionString,platform")
	q.Set("fields[apps]", "name,bundleId")
	q.Set("fields[builds]", "version")

	body, err := c.get(ctx, "/v1/appStoreVersions/"+url.PathEscape(versionID), q)
	if err != nil {
		return nil, err
	}

	var parsed versionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("asc: decode response: %w", err)
	}

	info := &VersionInfo{
		VersionString: parsed.Data.Attributes.VersionString,
		Platform:      parsed.Data.Attributes.Platform,
	}
	if app := parsed.Data.Relationships.App.Data; app != nil {
		info.AppID = app.ID
	}
	for _, inc := range parsed.Included {
		switch inc.Type {
		case "apps":
			info.AppName = inc.Attributes.Name
			info.BundleID = inc.Attributes.BundleID
			if info.AppID == "" {
				info.AppID = inc.ID
			}
		case "builds":
			info.BuildNumber = inc.Attributes.Version
		}
	}
	return info, nil
}

// buildResponse models the subset of a builds resource this client reads. The
// build number lives on the resource itself, the marketing version on the
// included preReleaseVersions entry.
type buildResponse struct {
	Data struct {
		Attributes struct {
			Version string `json:"version"`
		} `json:"attributes"`
		Relationships struct {
			App struct {
				Data *struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"app"`
		} `json:"relationships"`
	} `json:"data"`
	Included []struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Name     string `json:"name"`
			BundleID string `json:"bundleId"`
			Version  string `json:"version"`
			Platform string `json:"platform"`
		} `json:"attributes"`
	} `json:"included"`
}

// BuildInfo fetches the app, version and build number of a builds resource. A
// buildUploads resource shares its ID with the build it produced, so the same
// lookup answers for both.
func (c *Client) BuildInfo(ctx context.Context, buildID string) (*VersionInfo, error) {
	if buildID == "" {
		return nil, fmt.Errorf("asc: build ID is required")
	}

	q := url.Values{}
	q.Set("include", "app,preReleaseVersion")
	q.Set("fields[builds]", "version")
	q.Set("fields[apps]", "name,bundleId")
	q.Set("fields[preReleaseVersions]", "version,platform")

	body, err := c.get(ctx, "/v1/builds/"+url.PathEscape(buildID), q)
	if err != nil {
		return nil, err
	}

	var parsed buildResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("asc: decode response: %w", err)
	}

	info := &VersionInfo{BuildNumber: parsed.Data.Attributes.Version}
	if app := parsed.Data.Relationships.App.Data; app != nil {
		info.AppID = app.ID
	}
	for _, inc := range parsed.Included {
		switch inc.Type {
		case "apps":
			info.AppName = inc.Attributes.Name
			info.BundleID = inc.Attributes.BundleID
			if info.AppID == "" {
				info.AppID = inc.ID
			}
		case "preReleaseVersions":
			info.VersionString = inc.Attributes.Version
			info.Platform = inc.Attributes.Platform
		}
	}
	return info, nil
}
