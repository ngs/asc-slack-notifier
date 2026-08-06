BINARY      := asc-slack-notifier
PKG         := ./cmd/asc-slack-notifier
BIN_DIR     := bin
DIST_DIR    := dist
IMAGE       ?= asc-slack-notifier:latest
LAMBDA_ARCH ?= arm64
GO          ?= go

.PHONY: all
all: build

.PHONY: build
build:
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY) $(PKG)

.PHONY: run
run:
	$(GO) run $(PKG)

.PHONY: test
test:
	$(GO) test ./...

.PHONY: cover
cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: lint
lint: vet
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| echo "golangci-lint not installed; ran go vet only"

.PHONY: check
check: vet test

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE) .

# Builds the Lambda deployment package for the provided.al2023 runtime, which
# expects a binary named "bootstrap" at the root of the zip.
.PHONY: lambda-zip
lambda-zip:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(LAMBDA_ARCH) \
		$(GO) build -trimpath -ldflags="-s -w" -o $(DIST_DIR)/bootstrap $(PKG)
	cd $(DIST_DIR) && zip -q -j $(BINARY)-lambda-$(LAMBDA_ARCH).zip bootstrap
	@echo "built $(DIST_DIR)/$(BINARY)-lambda-$(LAMBDA_ARCH).zip"

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) coverage.out coverage.html
