.PHONY: all build build-static test clean

APP_NAME := ros-fleet
VERSION ?= 2.3.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "manual")
DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
	-X github.com/AndreyOkh/ros-fleet/internal/version.Version=$(VERSION) \
	-X github.com/AndreyOkh/ros-fleet/internal/version.Commit=$(COMMIT) \
	-X github.com/AndreyOkh/ros-fleet/internal/version.BuildDate=$(DATE) \
	-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

all: test build

build:
	go build -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/ros-fleet

build-static:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS) -extldflags '-static'" -o bin/$(APP_NAME)-linux-amd64 ./cmd/ros-fleet

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-linux-amd64 ./cmd/ros-fleet
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-linux-arm64 ./cmd/ros-fleet
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-darwin-arm64 ./cmd/ros-fleet
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME)-windows-amd64.exe ./cmd/ros-fleet

test:
	go test -v ./...

clean:
	rm -rf bin/
