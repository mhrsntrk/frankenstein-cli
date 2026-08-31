BINARY := frankenstein
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/mhrsntrk/frankenstein-cli/internal/cli.Version=$(VERSION)

# Pinned, so a staticcheck release cannot turn a green tree red without a commit
# that says so. CI runs this same target rather than keeping its own version.
STATICCHECK_VERSION := v0.8.1

# The packages copied from hey-cli, skipped by staticcheck. They carry helpers
# this client never calls and staticcheck's unused check flags every one of
# them; deleting them would make the files stop matching upstream, which is the
# whole reason they are kept verbatim. See NOTICE.
VENDORED_PKGS := /internal/(tui/render|tui/render/habit|terminal)$$

.PHONY: build build-all install test test-race vet fmt fmt-check tidy docs \
	snapshot staticcheck boundary check clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/frankenstein

# Compiles every package, including the ones the binary does not import.
build-all:
	go build ./...

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/frankenstein

test:
	go test ./...

# What CI runs. The race detector is where the TUI's tea.Cmd goroutines and the
# sync workers get caught.
test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# The formatting gate, kept apart from fmt: fmt rewrites the tree, so running it
# first would make this pass no matter what the tree looked like.
fmt-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "not gofmt-clean:"; echo "$$files"; exit 1; \
	fi

tidy:
	go mod tidy

docs:
	go run ./cmd/frankenstein docs --dir .gen

# Build every release artifact locally without publishing anything.
snapshot:
	goreleaser release --snapshot --clean

staticcheck:
	@go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) \
		$$(go list ./... | grep -Ev '$(VENDORED_PKGS)')

# The provider interface is the point of the design, so a leak is a build
# failure rather than a review comment.
boundary:
	@leaked=$$(grep -rl 'ProtonMail/go-proton-api' --include='*.go' internal/ cmd/ \
		| grep -vE 'internal/(mail/protonmail|auth)/' || true); \
	if [ -n "$$leaked" ]; then \
		echo "Proton types leaked outside the adapter:"; echo "$$leaked"; exit 1; \
	fi

# Every check CI runs, in the order CI runs them. Release tags run this target
# too, so a tag cannot ship what a pull request would have been blocked on.
check: fmt-check vet build-all test-race staticcheck boundary
	@echo "all checks passed"

clean:
	rm -rf $(BINARY) dist/ .gen/
