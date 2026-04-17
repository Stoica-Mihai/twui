BINARY := twui
CMD    := ./cmd/twui

.PHONY: all build run debug demo test test-race test-live coverage coverage-html vet lint fmt install release-dry clean

all: build

build:
	go build -o $(BINARY) $(CMD)

run: build
	./$(BINARY)

debug: build
	./$(BINARY) -v

# Run the TUI against hardcoded fixtures — no Twitch API, no media player,
# no real IRC. Useful for screenshots and zero-network smoke testing.
demo: build
	./$(BINARY) --demo

test:
	go test ./...

# Run tests with the race detector enabled. Default for pre-commit / CI.
test-race:
	go test -race ./...

# Run only the live Twitch API tests (network required). These double as
# canaries for GraphQL schema drift — the last such drift was caught by
# TestLive_CategoryStreams in 7a9bc94.
test-live:
	go test -v -run 'TestLive_' ./pkg/twitch/...

# Write a coverage profile to coverage.out and print a per-function summary.
coverage:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -n 1

# Open the HTML coverage report in the default browser.
coverage-html: coverage
	go tool cover -html=coverage.out

vet:
	go vet ./...

# Run golangci-lint with the config in .golangci.yml. Requires golangci-lint
# to be installed: https://golangci-lint.run/usage/install/
lint:
	golangci-lint run

# Format all Go sources in place. Run before committing.
fmt:
	gofmt -s -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w . || echo "goimports not installed, skipped"

# Install the binary to $GOBIN (or $GOPATH/bin).
install:
	go install $(CMD)

# Dry-run goreleaser: build snapshot binaries into ./dist without publishing.
# Useful for testing the release pipeline locally before tagging.
release-dry:
	goreleaser release --snapshot --clean

clean:
	rm -f $(BINARY)
	rm -rf dist coverage.out
