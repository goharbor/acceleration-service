GIT_COMMIT := $(shell git rev-list -1 HEAD)
BUILD_TIME := $(shell date -u +%Y%m%d.%H%M)
CWD := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

default:
	go build -ldflags '-X main.versionGitCommit=${GIT_COMMIT} -X main.versionBuildTime=${BUILD_TIME}' -gcflags=all="-N -l" ./cmd/acceleration-service
