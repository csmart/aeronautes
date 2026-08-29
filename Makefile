# Thin, conventional entry points. All real build logic lives in the
# Wails v3 Taskfiles (Taskfile.yml + build/), which `wails3 task` runs —
# keep it there, and keep this file a directory of one-liners.
#
# Prerequisites on Fedora (see README "Building on Fedora"):
#   sudo dnf install golang gcc gcc-c++ pkgconf gtk4-devel webkitgtk6.0-devel nodejs npm
#   make tools

# `go install` drops wails3 into $HOME/go/bin (default GOBIN).
export PATH := $(HOME)/go/bin:$(PATH)

WAILS3_VERSION := v3.0.0-beta.15

.PHONY: all tools build run dev test test-go test-frontend test-integration \
	rpm deb package spike rclone-matrix clean

all: build

## tools: install the pinned wails3 CLI
tools:
	go install github.com/wailsapp/wails/v3/cmd/wails3@$(WAILS3_VERSION)

## build: production binary (frontend + bindings + go) -> bin/aeronautes
build:
	wails3 task build

## run: build then launch
run: build
	./bin/aeronautes

## dev: hot-reload development mode
dev:
	wails3 task dev

## test: everything that runs without a cluster
test: test-go test-frontend

test-go:
	go test -race -count=1 ./...

test-frontend:
	cd frontend && npm install && npm run check && npm test

## test-integration: drive the adapter against a real containerized Swift.
## Start one first (creds test:tester/testing):
##   sudo podman run -d --name saio -p 127.0.0.1:8080:8080 docker.io/openstackswift/saio:latest
test-integration:
	AERONAUTES_SAIO_AUTH_URL=$${AERONAUTES_SAIO_AUTH_URL:-http://127.0.0.1:8080/auth/v1.0} \
		go test -race -count=1 -v ./internal/integration

## rpm: build bin/aeronautes.rpm (nfpm; installable with dnf install ./bin/aeronautes.rpm)
rpm:
	wails3 task linux:create:rpm

## deb: build the deb the same way
deb:
	wails3 task linux:create:deb

## package: rpm + deb + AppImage
package:
	wails3 task package

## rclone-matrix: §65.7 compatibility matrix (pin + latest) against the SAIO
rclone-matrix:
	./scripts/rclone-matrix.sh

## spike: §55 release-hardening check on the production binary
spike:
	./scripts/webview-spike.sh

clean:
	rm -rf bin frontend/dist
