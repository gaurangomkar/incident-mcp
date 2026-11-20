# incident-mcp developer tasks.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet lint fmt ci clean install

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/incident-mcp ./cmd/incident-mcp

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/incident-mcp

test:
	go test -count=1 -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	golangci-lint fmt ./...

ci: vet lint test build

clean:
	rm -rf bin dist
