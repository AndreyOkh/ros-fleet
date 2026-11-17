# Stage 1: Build binary
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Install certificates and git
RUN apk add --no-cache ca-certificates tzdata git

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Compile fully static binary without CGO
ARG VERSION=1.0.0
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /bin/mikrotik-backup \
    ./cmd/backup

# Stage 2: Minimal runtime image
FROM alpine:3.21

# Install CA certificates and tzdata for proper TLS and time handling
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 backup && \
    adduser -u 1000 -G backup -s /bin/sh -D backup

WORKDIR /app

# Copy binary from builder
COPY --from=builder /bin/mikrotik-backup /usr/local/bin/mikrotik-backup

# Prepare directories
RUN mkdir -p /app/backups && chown -R backup:backup /app

USER backup

VOLUME ["/app/backups"]

ENTRYPOINT ["/usr/local/bin/mikrotik-backup"]
CMD ["-config", "/app/config.json"]
