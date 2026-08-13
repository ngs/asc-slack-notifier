# asc-slack-notifier — Implementation Spec

A Go OSS service that receives App Store Connect webhook notifications and
posts them to Slack. Deployable to both **Cloud Run** (plain HTTP server) and
**AWS API Gateway + Lambda**, from a single binary switched by environment
variables.

- Repository: `/Users/nagase/src/asc-slack-notifier` (currently empty; git
  initialized, branch `master`)
- Module path: `go.ngs.io/asc-slack-notifier` (vanity import)
- Go 1.22+ (match the locally installed Go version in `go.mod`)
- License: MIT (Copyright (c) 2026 Atsushi NAGASE)
- All documentation in English (see `AGENTS.md`)

## Background: App Store Connect webhook behavior (from Apple docs)

- App Store Connect sends `HTTP POST` requests to the registered URL.
- Authentication: HMAC-SHA256. Apple computes an HMAC over the raw payload
  body using the shared `secret` registered with the webhook, and sends it as
  the header `x-apple-signature: hmacsha256=<hex digest>`. The receiver must
  recompute the HMAC-SHA256 hex digest over the raw body and compare in
  constant time.
  (Documented test vector: secret `This is my secret`, body `Hello, World!` →
  `7f062172b01cb00b53ca068614674a3d982a34062a0f5d37687d5e3377e54657`)
- Ping events: App Store Connect can send test pings (WebhookPing). Respond
  200; optionally post a lightweight "ping received" Slack message
  (controllable via env, see below). A ping is identifiable by the payload's
  `data.type` (ping variant) or the WebhookEvent's `ping: true` flag.
- Example notification payload (`appStoreVersionAppVersionStateUpdated`):

```json
{
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
        "data": { "type": "appStoreVersions", "id": "ad7e6298-..." }
      }
    }
  }
}
```

- Event type enum (`WebhookEventType`, SCREAMING_SNAKE for the registration
  API; the payload's `data.type` is the lowerCamelCase counterpart):
  - `ALTERNATIVE_DISTRIBUTION_PACKAGE_AVAILABLE_UPDATED`
  - `ALTERNATIVE_DISTRIBUTION_PACKAGE_VERSION_CREATED`
  - `ALTERNATIVE_DISTRIBUTION_TERRITORY_AVAILABILITY_UPDATED`
  - `APP_STORE_VERSION_APP_VERSION_STATE_UPDATED`
  - `BACKGROUND_ASSET_VERSION_APP_STORE_RELEASE_STATE_UPDATED`
  - `BACKGROUND_ASSET_VERSION_EXTERNAL_BETA_RELEASE_STATE_UPDATED`
  - `BACKGROUND_ASSET_VERSION_INTERNAL_BETA_RELEASE_CREATED`
  - `BACKGROUND_ASSET_VERSION_STATE_UPDATED`
  - `BETA_FEEDBACK_CRASH_SUBMISSION_CREATED`
  - `BETA_FEEDBACK_SCREENSHOT_SUBMISSION_CREATED`
  - `BUILD_BETA_DETAIL_EXTERNAL_BUILD_STATE_UPDATED`
  - `BUILD_UPLOAD_STATE_UPDATED`

## Architecture

Implement a single `http.Handler`; the startup mode decides how it is wrapped:

- **http mode (Cloud Run)**: serve with `net/http`. Port from `PORT`
  (default `8080`, Cloud Run convention).
- **lambda mode (API Gateway + Lambda)**: wrap the same `http.Handler` with
  `github.com/aws/aws-lambda-go/lambda` +
  `github.com/awslabs/aws-lambda-go-api-proxy/httpadapter`. Target API
  Gateway HTTP API (payload v2) → `httpadapter.NewV2()`.
  **Note**: API Gateway may base64-encode the body. httpadapter handles the
  decoding, but signature verification must run against the raw body bytes —
  cover this with a test.

Mode resolution (`RUN_MODE` env var):
1. If `RUN_MODE=http` or `RUN_MODE=lambda` is set explicitly, obey it.
2. Otherwise auto-detect: lambda if `AWS_LAMBDA_FUNCTION_NAME` is present,
   http otherwise.

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `ASC_WEBHOOK_SECRET` | ✔ | HMAC secret. Fatal at startup if unset |
| `SLACK_WEBHOOK_URL` | ✔ (either) | Slack Incoming Webhook URL |
| `SLACK_BOT_TOKEN` + `SLACK_CHANNEL` | ✔ (either) | Use chat.postMessage instead. `SLACK_WEBHOOK_URL` wins if both set |
| `RUN_MODE` | – | `http` / `lambda`. Auto-detected when unset |
| `PORT` | – | Port for http mode. Default `8080` |
| `WEBHOOK_PATH` | – | Receive path. Default `/webhook` |
| `HEALTH_PATH` | – | Health check path. Default `/health`. Not `/healthz`: the Google frontend in front of Cloud Run can intercept that path before it reaches the container |
| `NOTIFY_PING` | – | `false` suppresses Slack messages for pings. Default `true` |
| `LOG_LEVEL` | – | `debug`/`info`/`warn`/`error` via `log/slog`. Default `info` |
| `ASC_API_KEY_ID` | – | App Store Connect API key ID (`kid`). Presence enables enrichment |
| `ASC_API_ISSUER_ID` | – | App Store Connect API issuer ID (`iss`) |
| `ASC_API_PRIVATE_KEY` | – | PEM contents of the API key, plain or base64-encoded (auto-detected by the PEM armor). Literal `\n` sequences in a plain value are expanded to newlines |
| `ASC_API_PRIVATE_KEY_PATH` | – | Path to the `.p8` file, read at startup. `ASC_API_PRIVATE_KEY` wins if both are set |

Fatal at startup if no Slack destination is configured.

The four `ASC_API_*` variables form a group: none set disables enrichment, a
partial set is fatal at startup rather than a silent downgrade.

## Package layout

```
.
├── cmd/asc-slack-notifier/main.go   # mode resolution + startup only
├── internal/config/config.go        # env loading & validation
├── internal/webhook/
│   ├── handler.go                   # http.Handler (verify → parse → notify)
│   ├── signature.go                 # HMAC-SHA256 verification
│   ├── signature_test.go            # uses Apple's documented test vector
│   ├── payload.go                   # payload structs & parsing
│   ├── payload_test.go
│   └── handler_test.go              # httptest: ok / bad sig 401 / 405 / ping
├── internal/slack/
│   ├── client.go                    # Incoming Webhook & chat.postMessage
│   ├── message.go                   # event → Block Kit formatting
│   └── message_test.go
├── internal/asc/
│   ├── client.go                    # App Store Connect API (optional enrichment)
│   ├── token.go                     # ES256 JWT minting with the stdlib
│   ├── client_test.go
│   └── token_test.go
├── Dockerfile                       # multi-stage, distroless/scratch, non-root
├── Makefile                         # build / test / lint / docker-build / lambda-zip
├── .github/workflows/ci.yml         # go test + go vet (golangci-lint optional)
├── LICENSE                          # MIT
├── README.md                        # English; see acceptance criteria
├── .gitignore
├── AGENTS.md                        # repo guidelines (CLAUDE.md symlinks to it)
└── docs/SPEC.md                     # this file
```

Keep dependencies minimal: only `aws-lambda-go` and
`aws-lambda-go-api-proxy`. Post to Slack with plain `net/http` (no slack-go).

## Handler behavior

1. Anything other than `POST {WEBHOOK_PATH}` → 404/405. `GET {HEALTH_PATH}` →
   `200 ok` (for Cloud Run / LB health checks). `HEALTH_PATH` and
   `WEBHOOK_PATH` must differ; startup fails when they collide.
2. Read the body with a 1 MiB cap (`http.MaxBytesReader`).
3. Read the `x-apple-signature` header (case-insensitive). Format:
   `hmacsha256=<hex>`. Missing header, malformed value, or HMAC mismatch
   (`hmac.Equal`) → `401` with no reason in the body (log the reason).
4. Parse JSON, branch on `data.type`:
   - ping variant → return `200`; post a simple Slack message if
     `NOTIFY_PING=true`.
   - known event → format per event and notify Slack.
   - unknown type → warn log + generic formatting (raw type name plus
     attributes as key-value fields) and still notify. Never drop.
5. Slack post succeeded → `200`. Slack post failed → `502` (let App Store
   Connect's redelivery mechanism handle retries).
6. Always verify the signature against the **raw body bytes** (never a
   re-marshaled JSON string).

## Slack message formatting

Use Block Kit. Common format:
- Header: humanized event type (e.g. `App Store Version State Updated`).
- Fields: `oldValue` → `newValue` (for state-update events), timestamp,
  instance type/id from relationships.
- Emoji prefix by state (e.g. `READY_FOR_REVIEW` 📝, `PENDING_APPLE_RELEASE`
  ⏳, `READY_FOR_DISTRIBUTION`/`ACCEPTED` ✅, `REJECTED`/`FAILED`-ish ❌,
  otherwise ℹ️). Keep the mapping table-driven and local to `message.go`.
- No need for a case per event type: two generic formatters — "state updated"
  (has old/new) and "created" — plus the unknown-type fallback are enough.
  Title-case the lowerCamelCase `data.type` for the header.

## App Store Connect API enrichment (optional)

A webhook payload names the resource an event is about only as
`relationships.instance.data = {type: "appStoreVersions", id: <uuid>}` — or
`{type: "buildUploads", …}` for a build upload — so the app, version string and
build number have to be fetched from the App Store Connect API.

- `internal/asc` is a minimal client for that API. It signs an ES256 JWT with
  the stdlib (`crypto/ecdsa`, `crypto/x509`, `encoding/pem`) — no JWT library —
  caching the token until a minute before its ten-minute expiry, and issues
  `GET /v1/appStoreVersions/{id}?include=app,build` with the field sets narrowed
  to what is rendered. Both PKCS#8 (Apple's `.p8`) and SEC1 private keys parse.
- A second lookup, `GET /v1/builds/{id}?include=app,preReleaseVersion`, covers
  build events. A `buildUploads` resource has no `app` relationship of its own,
  but **its ID is the ID of the build it produced**, so the build endpoint
  resolves a `buildUploads` instance directly — no intermediate request. The
  build number is `data.attributes.version`; the marketing version and platform
  come from the included `preReleaseVersions` entry, and the app from the
  included `apps` entry (the `app` relationship data is often null).
- `slack.Enricher` is the seam: `slack.Client` calls `EnrichAppStoreVersion` for
  an `appStoreVersions` instance and `EnrichBuild` for a `buildUploads` or
  `builds` instance, and `cmd/asc-slack-notifier` adapts `asc.Client` to it.
  Every field is best effort — a version with no binary attached has no build,
  which yields an empty string rather than an error.
- An enriched message leads with `App`, `Version` and `Build` fields, drops the
  raw instance UUID field, and gains an actions block with an "Open in App Store
  Connect" button linking to
  `https://appstoreconnect.apple.com/apps/{appId}/distribution`, or, for build
  events, to `.../apps/{appId}/testflight/{platform}` (`IOS` → `ios`, `MAC_OS` →
  `macos`, `TV_OS` → `tvos`, `VISION_OS` → `visionos`; an unknown platform drops
  the segment). The fallback
  text names the app and version too, so notifications and channel lists are
  readable without opening the message.
- Enrichment never blocks a notification: a lookup failure is logged at warn
  level and the message is posted from the payload alone. With no credentials
  configured the rendering is identical to a build without this feature.

## Tests & acceptance criteria

- `go build ./...`, `go test ./...`, `go vet ./...` all pass.
- `signature_test.go` uses Apple's documented vector (secret
  `This is my secret`, body `Hello, World!` →
  `7f062172b01cb00b53ca068614674a3d982a34062a0f5d37687d5e3377e54657`), plus
  mismatch, missing header, and bad prefix cases.
- `handler_test.go` covers 200 / 401 / 405 / health check / 502 on Slack failure
  via httptest. Slack client is an interface with a mock injected.
- Dockerfile is multi-stage and the final image runs as non-root.
- README (English) includes: overview, Configuration (env table), Deploy to
  Cloud Run (`gcloud` example), Deploy to AWS Lambda + API Gateway (zip build
  for the `provided.al2023` runtime, or a SAM example), registering the
  webhook with App Store Connect (`POST /v1/webhooks` curl example per the
  Apple spec above), and testing via the webhook ping endpoint.
- Lambda build: `make lambda-zip` produces a zip containing a `bootstrap`
  binary built with `GOOS=linux GOARCH=arm64`.
- Do not commit; the user commits explicitly.
