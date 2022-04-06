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
- Endpoint URL: `<acceleration service address>`/api/v1/conversions
- Auth Header: `<configured in acceleration service>`

2. Add robot account

Administration -> Robot Accounts -> `+ NEW ROBOT ACCOUNT`, add robot account with following fields:

- Expiration time: `<by your choice>`
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
$ ps aux | grep containerd
root        929  0.5  0.5 2222284 22368 ?       Ssl  Sep16   4:27 /usr/local/bin/containerd
```

2. Boot daemon to serve webhook request in PUSH_ARTIFACT event

``` shell
$ ./acceld --config ./misc/config/config.yaml
```

### Test image conversion

1. Trigger image conversion

We can either push an image to Harbor:

``` shell
$ docker push <harbor-service-address>/library/nginx:latest
```

Or convert an existing image manually using the `accelctl` CLI tool:

``` shell
$ ./accelctl task create <harbor-service-address>/library/nginx:latest
$ ./accelctl task list
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
INFO[2021-09-17T05:44:04.317992888Z] converted image 192.168.1.1/library/nginx:latest-nydus  module=converter
```

After that, we will find converted artifact in Harbor interface with name `<harbor-service-address>/nginx:latest-nydus`.

### One-time mode

One-time mode allows to do a conversion without starting the `acceld` service (still requires a local containerd service), using `accelctl` like this:

```
$ accelctl convert --config ./misc/config/config.yaml 192.168.1.1/library/nginx:latest

INFO[2022-01-28T03:39:28.039029557Z] pulling image 192.168.1.1/library/nginx:latest     module=converter
INFO[2022-01-28T03:39:28.075375146Z] pulled image 192.168.1.1/library/nginx:latest      module=converter
INFO[2022-01-28T03:39:28.075530522Z] converting image 192.168.1.1/library/nginx:latest  module=converter
INFO[2022-01-28T03:39:29.561103924Z] converted image 192.168.1.1/library/nginx:latest-nydus  module=converter
INFO[2022-01-28T03:39:29.561197593Z] pushing image 192.168.1.1/library/nginx:latest-nydus  module=converter
INFO[2022-01-28T03:39:29.587585066Z] pushed image 192.168.1.1/library/nginx:latest-nydus  module=converter
```

## Driver

### Interface

Acceleration Service Framework provides a built-in extensible method called driver, which allows the integration of various types of accelerated image format conversions, the framework will automatically handle operations such as pulling source image and pushing converted image, the format providers need to implement the following interface in [pkg/driver](./pkg/driver):

```golang
// Driver defines image conversion interface, the following
// methods need to be implemented by image format providers.
type Driver interface {
	// Convert converts the source image to target image, where
	// content parameter provides necessary image utils, image
	// content store and so on. If conversion successful, the
	// converted image manifest will be returned, otherwise a
	// non-nil error will be returned.
	Convert(context.Context, content.Provider) (*ocispec.Descriptor, error)

	// Name gets the driver type name, it is used to identify
	// different accelerated image formats.
	Name() string

	// Version gets the driver version, it is used to identify
	// different accelerated image format versions with same driver.
	Version() string
}
```

### Testing

We can specify the driver name by modifying `converter.driver` in the configuration file, and modify the fields in `converter.config` to specify the driver-related configuration, see [example configuration file](./misc/config/config.yaml.estargz.tmpl).

## API

Acceld acts as an HTTP server to serve webhook HTTP requests from Harbor for image conversions, currently acceld exposes following APIs:

### Create Task

#### Request

```
POST /api/v1/conversions?sync=$sync

{
    "type": "PUSH_ARTIFACT",
    "event_data": {
        "resources": [
            {
                "resource_url": "192.168.1.1/library/nginx:latest"
            }
        ]
    }
}
```

`$sync`: boolean, `true` enable waiting for api to respond until the conversion task is completed.

#### Response

```
Ok
```

| Status | Description                                  |
| ------ | -------------------------------------------- |
| 200    | Task created/Task finished                   |
| 400    | Illegal parameter                            |
| 401    | Unauthorized, invalid `Authorization` header |

### List Task

#### Request

```
GET /api/v1/conversions
```

#### Response

```
[
    {
        "id": "5bdf7e80-1f1e-461f-a50d-f41d27434662",
        "created": "2022-04-06T06:45:11.83226503Z",
        "finished": "2022-04-06T06:45:11.948393604Z",
        "source": "192.168.1.1/library/nginx:latest",
        "status": "$status",
        "reason": "$reason"
    }
]
```

`$status`: string, possible values is `PROCESSING`, `COMPLETED`, `FAILED`.

`$reason`: string, giving failed reason message when the status is `FAILED`.

| Status | Description                                  |
| ------ | -------------------------------------------- |
| 200    | Return task list                             |
| 401    | Unauthorized, invalid `Authorization` header |
