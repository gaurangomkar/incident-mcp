# syntax=docker/dockerfile:1

FROM golang:1.27.4-alpine AS build
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/incident-mcp ./cmd/incident-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/incident-mcp /incident-mcp
USER nonroot:nonroot
ENTRYPOINT ["/incident-mcp"]
