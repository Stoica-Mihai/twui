BINARY := twui
CMD    := ./cmd/twui

.PHONY: all build run debug test vet clean

all: build

build:
	go build -o $(BINARY) $(CMD)

run: build
	./$(BINARY)

debug: build
	./$(BINARY) -v

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
