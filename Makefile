# incident-mcp developer tasks.

.PHONY: test fmt

test:
	go test ./...

fmt:
	gofmt -w .
