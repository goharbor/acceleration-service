# Acceleration Service

Acceleration Service provides a general service to Harbor with the ability to automatically convert user images to accelerated images. When a user does something such as artifact push, Harbor will request the service to complete the corresponding image conversion through its integrated Nydus, eStargz, etc. drivers.

⚠️ This project is still in progress, see more details in this [proposal](https://github.com/goharbor/community/pull/167).

## Quickstart

### Build binary

``` shell
$ make
```

### Configuration (Harbor side)

Login to Harbor web interface to complete the following operation.

1. Add webhook configuration

Projects -> Webhooks -> `+ NEW WEBHOOK`, add webhook configuration with following fields:

- Notify Type: http
- Event Type: artifact pushed
- Endpoint URL: <acceleration service address>/api/v1/conversions
- Auth Header: <configured in acceleration service>

2. Add robot account

Administration -> Robot Accounts -> `+ NEW ROBOT ACCOUNT`, add robot account with following fields:

- Expiration time: <by your chose>
- Reset permissions: select `Push Artifact`, `Pull Artifact`, `Create Tag`

Generate a base64 encoded auth string using robot account name and secret like this:

``` shell
$ printf '<robot-name>:<robot-secret>' | base64
```

### Configuration (Acceleration service side)

1. Copy template config file

nydus:

``` shell
$ cp misc/config/config.yaml.nydus.tmpl misc/config/config.yaml
```

estargz:

``` shell
$ cp misc/config/config.yaml.estargz.tmpl misc/config/config.yaml
```

2. Edit config file

Following comments in `misc/config/config.yaml`, ensure base64 encoded auth string and webhook auth header be configured correctly.

### Boot service

1. Ensure containerd service is running

Acceleration service depends on containerd service to manage image content, containerd service is used default if you have installed the latest docker.

``` shell
$ ps aux | grep contained
root        929  0.5  0.5 2222284 22368 ?       Ssl  Sep16   4:27 /usr/local/bin/containerd
```

2. Boot daemon to serve webhook request in PUSH_ARTIFACT event

``` shell
$ ./acceleration-service --config ./misc/config/config.yaml
```

### Test with pushing image

1. Push a test image to Harbor

``` shell
$ docker push <harbor-service-address>/library/nginx:latest
```

2. Got converted image

If everything is ready, we can see the log output in acceleration service like this:

``` shell
INFO[2021-09-17T05:43:49.783769943Z] Version: ecbfefc312ea78d81eb895ef7768a60caa30a281.20210917.0543
INFO[2021-09-17T05:43:49.786146553Z] [API] HTTP server started on 0.0.0.0:2077
INFO[2021-09-17T05:44:01.82592358Z] received webhook request from 192.168.1.1:46664  module=api
INFO[2021-09-17T05:44:01.826490232Z] POST /api/v1/conversions 200 552.695µs 392>5bytes 192.168.1.1  module=api
INFO[2021-09-17T05:44:01.830893103Z] pulling image 192.168.1.1/library/nginx:latest  module=converter
INFO[2021-09-17T05:44:01.916123236Z] pulled image 192.168.1.1/library/nginx:latest  module=converter
INFO[2021-09-17T05:44:01.916175337Z] converting image 192.168.1.1/library/nginx:latest  module=converter
INFO[2021-09-17T05:44:04.317992888Z] converted image 192.168.1.1/library/nginx:latest-converted  module=converter
```

After that, we will find converted artifact in Harbor interface with name `<harbor-service-address>/nginx:latest-converted`.
