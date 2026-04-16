BINARY := twui
CMD    := ./cmd/twui

.PHONY: all build run test vet clean

all: build

build:
	go build -o $(BINARY) $(CMD)

run: build
	./$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
