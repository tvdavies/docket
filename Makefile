VERSION ?= dev
LDFLAGS := -s -w -X github.com/tvdavies/docket/internal/cli.Version=$(VERSION)
PREFIX  ?= $(HOME)/.local

MANDIR  ?= man

.PHONY: build test fmt fmt-check vet install man install-man clean snapshot

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/docket .

test:
	go test ./...

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/docket $(PREFIX)/bin/docket

# Roff man pages, one per command, generated from the command help.
man:
	go run -ldflags '$(LDFLAGS)' . man --dir $(MANDIR)

install-man: man
	install -d $(PREFIX)/share/man/man1
	install -m 0644 $(MANDIR)/*.1 $(PREFIX)/share/man/man1/

# Cross-platform release build (requires goreleaser).
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist $(MANDIR)
