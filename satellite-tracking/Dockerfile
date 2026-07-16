# syntax=docker/dockerfile:1
FROM golang:1.21-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o satellite-tracking .

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/satellite-tracking /usr/local/bin/satellite-tracking

ENTRYPOINT ["/usr/local/bin/satellite-tracking"]
