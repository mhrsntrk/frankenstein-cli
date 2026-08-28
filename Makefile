BINARY := frankenstein
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/mhrsntrk/frankenstein-cli/internal/cli.Version=$(VERSION)

.PHONY: build install test vet fmt clean tidy docs snapshot check

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

docs:
	go run ./cmd/frankenstein docs --dir .gen

# Build every release artifact locally without publishing anything.
snapshot:
	goreleaser release --snapshot --clean

# What CI runs.
check: fmt vet test
	@gofmt -l . | tee /dev/stderr | (! read)
	@leaked=$$(grep -rl 'ProtonMail/go-proton-api' --include='*.go' internal/ cmd/ \
		| grep -vE 'internal/(mail/protonmail|auth)/' || true); \
	if [ -n "$$leaked" ]; then \
		echo "Proton types leaked outside the adapter:"; echo "$$leaked"; exit 1; \
	fi
	@echo "all checks passed"

clean:
	rm -rf $(BINARY) dist/ .gen/
