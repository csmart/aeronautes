# Thin, conventional entry points. All real build logic lives in the
# Wails v3 Taskfiles (Taskfile.yml + build/), which `wails3 task` runs —
# keep it there, and keep this file a directory of one-liners.
#
# Prerequisites on Fedora (see README "Building on Fedora"):
#   sudo dnf install golang gcc gcc-c++ pkgconf gtk4-devel webkitgtk6.0-devel nodejs npm
#   make tools

# `go install` drops wails3 into $HOME/go/bin (default GOBIN).
export PATH := $(HOME)/go/bin:$(PATH)

WAILS3_VERSION := v3.0.0-beta.19

.PHONY: all tools build run dev test test-go test-frontend test-integration \
	verify-swift-source test-swift-core test-secret-service \
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

## coverage: combined Go coverage (SAIO up = full picture) + frontend coverage
coverage:
	set -o pipefail; go test -count=1 -coverprofile=cover.out -coverpkg=./internal/... ./... | grep -v "no test files"
	@go tool cover -func=cover.out | awk '/^total:/ {print "Go combined:", $$3}'
	cd frontend && npm run test:coverage | tail -8

test-go: verify-swift-source test-swift-core
	go test -race -count=1 ./...

verify-swift-source:
	python3 scripts/check-swift-source.py
	python3 -B scripts/check-swift-source-test.py

## The nested module is excluded by ./...; its rs suite needs live Rackspace CDN.
test-swift-core:
	cd third_party/ncw-swift/source && SWIFT_API_USER= SWIFT_API_KEY= SWIFT_AUTH_URL= go test -race -count=1 .

## A private Linux protocol fixture cannot substitute for native keychain tests.
test-secret-service:
	AERONAUTES_REQUIRE_SECRET_SERVICE_FIXTURE=1 go test -race -count=1 ./internal/secrets/protocoltest

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
