# syntax=docker/dockerfile:1
FROM golang:1.21-bookworm AS builder

WORKDIR /build
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o energy-grid .

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/energy-grid /usr/local/bin/energy-grid

ENTRYPOINT ["/usr/local/bin/energy-grid"]
