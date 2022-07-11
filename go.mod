module github.com/goharbor/acceleration-service

go 1.16

require (
	github.com/aliyun/aliyun-oss-go-sdk v0.0.0-20190307165228-86c17b95fcd5
	github.com/containerd/containerd v1.6.6
	github.com/containerd/nydus-snapshotter v0.3.0-alpha.4
	github.com/containerd/stargz-snapshotter v0.9.0
	github.com/containerd/stargz-snapshotter/estargz v0.11.4
	github.com/docker/cli v20.10.9+incompatible
	github.com/goharbor/harbor/src v0.0.0-20211021012518-bc6a7f65a6fa
	github.com/google/uuid v1.2.0
	github.com/labstack/echo-contrib v0.11.0
	github.com/labstack/echo/v4 v4.6.1
	github.com/labstack/gommon v0.3.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.3-0.20211202183452-c5a74bcca799
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.1
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/urfave/cli/v2 v2.3.0
	golang.org/x/time v0.0.0-20211116232009-f0f3c7e86c11 // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
)
