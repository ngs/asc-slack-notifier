# Repository Guidelines

## Language

- All documentation in this repository (README, docs/, code comments, commit
  messages, PR titles/bodies) is written in **English**.

## Project

- Go service that receives App Store Connect webhook notifications and posts
  them to Slack. Deployable to Cloud Run (HTTP server) or AWS API Gateway +
  Lambda, switched via environment variables. See `docs/SPEC.md`.
- Module path: `go.ngs.io/asc-slack-notifier` (vanity import; hosted on GitHub).
- License: MIT.

## Conventions

- Keep dependencies minimal: standard library first; `aws-lambda-go` and
  `aws-lambda-go-api-proxy` are the only expected third-party deps.
- Structured logging via `log/slog`.
- `go build ./...`, `go test ./...`, and `go vet ./...` must pass before any
  commit.
- Do not commit or push without explicit user instruction.
