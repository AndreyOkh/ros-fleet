# Stage 1: Build binary
FROM golang:1.27.4-alpine AS builder

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
    -o /bin/ros-fleet \
    ./cmd/ros-fleet

# Stage 2: Minimal runtime image
FROM alpine:3.21

# Install CA certificates, tzdata, git and openssh-client for runtime operations
RUN apk add --no-cache ca-certificates tzdata git openssh-client && \
    addgroup -g 1000 rosfleet && \
    adduser -u 1000 -G rosfleet -s /bin/sh -D rosfleet

WORKDIR /app

# Copy binary from builder
COPY --from=builder /bin/ros-fleet /usr/local/bin/ros-fleet

# Prepare directories and SSH home
RUN mkdir -p /app/backups/binary /app/backups/exports /home/rosfleet/.ssh && \
    chown -R rosfleet:rosfleet /app /home/rosfleet && \
    chmod 700 /home/rosfleet/.ssh

USER rosfleet

EXPOSE 8080

VOLUME ["/app/backups"]

ENTRYPOINT ["/usr/local/bin/ros-fleet"]
CMD ["-config", "/app/config.json"]
