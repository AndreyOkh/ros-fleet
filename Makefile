.PHONY: all build build-static test lint clean docker-build

APP_NAME := real-mikrotik-backup
VERSION ?= 1.0.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "manual")
DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

all: test build

build:
	go build -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/backup

build-static:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS) -extldflags '-static'" -o bin/$(APP_NAME)-linux-amd64 ./cmd/backup

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-linux-amd64 ./cmd/backup
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-linux-arm64 ./cmd/backup
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-darwin-arm64 ./cmd/backup
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-windows-amd64.exe ./cmd/backup

test:
	go test -v -race ./...

clean:
	rm -rf bin/
