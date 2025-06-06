# Acceleration Service

Acceleration Service provides a general service to Harbor with the ability to automatically convert user images to accelerated images. When a user does something such as artifact push, Harbor will request the service to complete the corresponding image conversion through its integrated [Nydus](https://github.com/dragonflyoss/image-service),
[eStargz, zstdchunked](https://github.com/containerd/stargz-snapshotter) etc. drivers.

[![Release Version](https://img.shields.io/github/v/release/goharbor/acceleration-service?style=flat)](https://github.com/goharbor/acceleration-service/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/goharbor/harbor-acceld.svg)](https://hub.docker.com/r/goharbor/harbor-acceld/)
[![Integration Test](https://github.com/goharbor/acceleration-service/actions/workflows/integration-test.yml/badge.svg?branch=main)](https://github.com/goharbor/acceleration-service/actions/workflows/integration-test.yml)
[![Concurrent Test](https://github.com/goharbor/acceleration-service/actions/workflows/concurrent-test.yml/badge.svg?branch=main)](https://github.com/goharbor/acceleration-service/actions/workflows/concurrent-test.yml)
[![Webhook Test](https://github.com/goharbor/acceleration-service/actions/workflows/webhook-test.yml/badge.svg?branch=main)](https://github.com/goharbor/acceleration-service/actions/workflows/webhook-test.yml)

See more details in the [design](./docs/design.md) doc.

## Quickstart
### GETTING STARTED

#### Get Harbor

Deploy a local harbor service if you don't have one, please refer to the harbor [documentation](https://goharbor.io/docs/latest/install-config).

#### Get binaries from release page

Currently, Acceleration Service includes the following tools:

- An `acceld` daemon to work as an HTTP service to handle image conversion requests from harbor or `accelctl`.
- An `accelctl` CLI tool to manage acceleration service (`acceld`) and can do image conversion in one-time mode.

Get `accelctl` and `acceld` binaries from [acceleration-service release](https://github.com/goharbor/acceleration-service/releases/latest).

### Configuration

#### Configure Habor

1. Login to the Harbor web interface.
2. Select one project and add a new Webhook configuration with the following fields:
    * Notify Type: choose HTTP
    * Event Type: Enable artifact pushed
    * Endpoint URL: `<acceleration service address>`/api/v1/conversions
    * Auth Header: `<configured in acceleration service>`
    > Note: The webhook can help to convert images automatically by acceleration service.
    > Also you can trigger an image conversion task by sending an HTTP request manually or using accelctl.

3. Create a system robot account with following fields:
    * Expiration time: `<by your choice>`
    * Reset permissions: select Push Artifact, Pull Artifact, Create Tag

    When you get the robot account `robot$<robot-name>`, please copy the secret and
    generate a base64 encoded auth string like this:

    ```bash
    $ echo -n '<robot-name>:<robot-secret>' | base64
    ```
    > Note: the encoded auth string will be used in configuring acceleration service on the next step.

#### Configure Acceleration Service

1. Copy the [template config file](https://github.com/goharbor/acceleration-service/tree/main/misc/config).
2. Modify the config file.
    * Change `provider.source` with your own harbor service hostname, the `auth` and
    `webhook.auth_header` should also be configured as the one generated by the above step.
    * Change settings in the `converter.driver` filed according to your requirements.
    > Please follow the comments in the template config file.

### Convert Image with Acceleration Service

#### Convert by acceld service
1. Boot acceld daemon in config file directory
    ```bash
    $ ./acceld --config ./config.yaml
    ```
2. Trigger image conversion
    * Push an image to trigger webhook.
    ```bash
    $ docker push <harbor-service-address>/library/nginx:latest
    ```
    * Convert manually by `accelctl` CLI tool.
    > Please make sure the source OCI v1 images exist in your harbor registry.
    ```bash
    $ ./accelctl task create <harbor-service-address>/library/nginx:latest
    ```
    Or you can create a conversion task over the HTTP API by `curl`. Please
    refer to the [development document](./docs/development.md#api).
    ```bash
    $ curl --location 'http://<acceleration-service-address>/api/v1/conversions?sync=$snyc' \
        --header 'Content-Type: application/json' \
        --data '{
            "type": "PUSH_ARTIFACT",
            "event_data": {
                "resources": [
                    {
                        "resource_url": "<harbor-service-address>/dfns/alpine:latest"
                    }
                ]
            }
        }
        '
    ```

#### One-time mode

One-time mode allows to do a conversion without starting the acceld service, using accelctl like this:
```
$ ./accelctl convert --config ./config.yaml 192.168.1.1/library/nginx:latest

INFO[2022-01-28T03:39:28.039029557Z] pulling image 192.168.1.1/library/nginx:latest     module=converter
INFO[2022-01-28T03:39:28.075375146Z] pulled image 192.168.1.1/library/nginx:latest      module=converter
INFO[2022-01-28T03:39:28.075530522Z] converting image 192.168.1.1/library/nginx:latest  module=converter
INFO[2022-01-28T03:39:29.561103924Z] converted image 192.168.1.1/library/nginx:latest-nydus  module=converter
INFO[2022-01-28T03:39:29.561197593Z] pushing image 192.168.1.1/library/nginx:latest-nydus  module=converter
INFO[2022-01-28T03:39:29.587585066Z] pushed image 192.168.1.1/library/nginx:latest-nydus  module=converter
```

### Check Converted Image

You can see the converted image and source oci image in the some repo, they have different tag suffix.

## Documentation

* [Development](./docs/development.md)
* [Garbage Collection](./docs/garbage-collection.md)
