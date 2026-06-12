VERSION ?= dev
LDFLAGS := -s -w -X github.com/tvdavies/tadu/internal/cli.Version=$(VERSION)
PREFIX  ?= $(HOME)/.local

.PHONY: build test fmt vet install clean snapshot

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/tadu .

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/tadu $(PREFIX)/bin/tadu

# Cross-platform release build (requires goreleaser).
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
