BINARY := twui
CMD    := ./cmd/twui

.PHONY: all build run debug test test-live vet clean

all: build

build:
	go build -o $(BINARY) $(CMD)

run: build
	./$(BINARY)

debug: build
	./$(BINARY) -v

test:
	go test ./...

# Run only the live Twitch API tests (network required). These double as
# canaries for GraphQL schema drift — the last such drift was caught by
# TestLive_CategoryStreams in 7a9bc94.
test-live:
	go test -v -run 'TestLive_' ./pkg/twitch/...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
