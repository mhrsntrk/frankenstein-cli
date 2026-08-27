BINARY := frankenstein
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/mhrsntrk/frankenstein-cli/internal/cli.Version=$(VERSION)

.PHONY: build install test vet fmt clean tidy

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/frankenstein

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/frankenstein

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
