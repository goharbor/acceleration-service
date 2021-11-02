module github.com/goharbor/acceleration-service

go 1.16

require (
	github.com/containerd/containerd v1.5.7
	github.com/containerd/stargz-snapshotter v0.9.0
	github.com/containerd/stargz-snapshotter/estargz v0.9.0
	github.com/docker/cli v20.10.9+incompatible
	github.com/goharbor/harbor/src v0.0.0-20211021012518-bc6a7f65a6fa
	github.com/google/uuid v1.2.0
	github.com/labstack/echo-contrib v0.11.0
	github.com/labstack/echo/v4 v4.6.1
	github.com/labstack/gommon v0.3.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.2-0.20210819154149-5ad6f50d6283
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/urfave/cli/v2 v2.3.0
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
)
