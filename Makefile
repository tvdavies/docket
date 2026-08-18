VERSION ?= dev
LDFLAGS := -s -w -X github.com/tvdavies/docket/internal/cli.Version=$(VERSION)
PREFIX  ?= $(HOME)/.local

.PHONY: build test test-web fmt vet install clean snapshot

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/docket .

test:
	go test ./...
	bun test internal/service/web/*.test.js

test-web:
	bun test internal/service/web/*.test.js

fmt:
	gofmt -w .

vet:
	go vet ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/docket $(PREFIX)/bin/docket

# Cross-platform release build (requires goreleaser).
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
