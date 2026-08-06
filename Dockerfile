# syntax=docker/dockerfile:1

FROM golang:1.26.4 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/asc-slack-notifier ./cmd/asc-slack-notifier

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/asc-slack-notifier /asc-slack-notifier

# distroless "nonroot" runs as uid/gid 65532.
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/asc-slack-notifier"]
