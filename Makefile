VERSION_TAG=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
GIT_COMMIT := $(shell git rev-list -1 HEAD)
BUILD_TIME := $(shell date -u +%Y%m%d.%H%M)

ACCELD_CONFIG ?= misc/config/config.nydus.yaml
ACCELD_SYSTEMD_UNIT_SERVICE ?= misc/acceleration-daemon.service

default: build

# Build binary to ./
build:
	go build -ldflags '-X main.versionTag=${VERSION_TAG} -X main.versionGitCommit=${GIT_COMMIT} -X main.versionBuildTime=${BUILD_TIME}' -gcflags=all="-N -l" ./cmd/acceld
	go build -ldflags '-X main.versionTag=${VERSION_TAG} -X main.versionGitCommit=${GIT_COMMIT} -X main.versionBuildTime=${BUILD_TIME}' -gcflags=all="-N -l" ./cmd/accelctl

install-check-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.59.1

check:
	@echo "$@"
	@$(shell go env GOPATH)/bin/golangci-lint run

# Run unit testing
# Run a particular test in this way:
# go test -v -count=1 -run TestFoo ./pkg/...
ut: default
	go test -count=1 -v ./pkg/...

# Run integration testing
smoke: default
	go test -count=1 -v ./test

# Run testing 
test: default ut smoke

release-image:
	docker build -t goharbor/harbor-acceld -f script/release/Dockerfile .

install:
	@echo "+ $@ ./acceld"
	@if [ ! -d /usr/local/bin ]; then mkdir -p /usr/local/bin; fi
	@sudo install -D -m 755 ./acceld /usr/local/bin/acceld

	@if [ ! -d /etc/acceld ]; then mkdir -p /etc/acceld; fi
	@if [ ! -e /etc/acceld/config.yaml ]; then echo "+ $@ ${ACCELD_CONFIG}"; sudo install -D -m 664 ${ACCELD_CONFIG} /etc/acceld/config.yaml ; fi

	@echo "+ $@ ${ACCELD_SYSTEMD_UNIT_SERVICE}"
	@sudo install -D -m 644 ${ACCELD_SYSTEMD_UNIT_SERVICE} /etc/systemd/system/acceleration-daemon.service
	@if which systemctl >/dev/null; then sudo systemctl enable /etc/systemd/system/acceleration-daemon.service; sudo systemctl restart acceleration-daemon; fi
