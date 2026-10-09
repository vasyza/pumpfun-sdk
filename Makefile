GO ?= go
GOTOOLCHAIN ?= auto
export GOTOOLCHAIN
GOFMT := $(shell $(GO) env GOROOT)/bin/gofmt
LINT_VERSION := v2.14.0
LINT := $(CURDIR)/bin/golangci-lint

.PHONY: build install test live-test vet fmt fmt-check lint lint-install check clean

build:
	$(GO) build ./...
	$(GO) build -trimpath -o bin/pumpfun ./cmd/pumpfun

install:
	$(GO) install ./cmd/pumpfun

test:
	$(GO) test -race ./...

live-test:
	$(GO) test -tags live -run '^TestLiveRead$$' ./internal/client

vet:
	$(GO) vet ./...

fmt:
	"$(GOFMT)" -w .

fmt-check:
	@files="$$('$(GOFMT)' -l .)"; if test -n "$$files"; then echo "$$files"; exit 1; fi

lint-install:
	GOBIN="$(CURDIR)/bin" GOTOOLCHAIN=go1.27.1 $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)

lint: lint-install
	"$(LINT)" run ./...

check: fmt-check vet test build lint

clean:
	rm -rf bin coverage.out coverage.html
