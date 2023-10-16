# Background

Pulling image is one of the time-consuming steps in the container lifecycle. Research shows that time to take for pull operation accounts for 76% of container startup time [FAST '16](https://www.usenix.org/node/194431). The P2P-based file distribution system like [Dragonfly](https://d7y.io/) can help reduce network latency when pulling images at runtime, saving network bandwidth and greatly reducing the impact of registry single point of failure, which is very important for large-scale container scaling scenarios.

This research [FAST '16](https://www.usenix.org/node/194431) also shows that only about 6.4% of the data is read at runtime, meaning that most of the data takes up and wastes network bandwidth and container startup time when pulling images or doing advance preheat of images. Accelerated image formats like [Nydus](https://github.com/dragonflyoss/image-service) and [eStargz](https://github.com/containerd/stargz-snapshotter) aim to address this issue, and they do a lot of work on data de-duplication, end-to-end data integrity, and the compatibility for OCI artifacts spec and distribution spec.

Harbor already supports push/lazily pull (using HTTP GET with [Range](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Range) header) accelerated image. And users can use the [nerdctl](https://github.com/containerd/nerdctl/blob/main/docs/nydus.md) or [nydusify](https://github.com/dragonflyoss/image-service/blob/master/docs/nydusify.md) tools to convert image to Nydus or eStargz image.

To make Harbor support users transparently use accelerated images, we needed a way to automatically convert user images to accelerated images, so the acceleration service (acceld) project was born, acceld provides a general service to Harbor with the ability to automatically convert to accelerated images. When a user does something such as artifact push, Harbor will send a webhook request to the service to complete the corresponding image conversion through its integrated Nydus, eStargz, etc. drivers. Harbor combines the P2P system and accelerated image format to provide a large-scale efficient, secure joint image solution for the container ecosystem.

# Implementation

## Workflow

- Create a new HTTP type webhook, set the endpoint URL provided by acceld, allows to configure when to trigger image conversion based on event types (such as artifact push) and filter conditions (such as repository name);
- When a user pushes an image that meets the webhook rules for conversion, a webhook request will be sent to acceld, wait a while, and the acceld will complete the conversion.

## Webhook

The design principle is to decouple the conversion service from the Harbor core as much as possible to keep it independent, with acceld acting as a webhook server and Harbor using webhook request to notify the acceld service to trigger image conversion action. For example, an accelerated image conversion can be triggered when an artifact push event occurs, involving the following webhook configuration from Harbor side:

- Notify Type: Acceld acts as an HTTP server to listen for webhook requests, so select HTTP;
- Event Type: Usually the artifact push is chosen, but other options can also be considered;
- Endpoint URL: The endpoint address of acceld HTTP server listens;
- Auth Header: The HTTP auth header checks configured on the acceld side are used by the ICS to verify the webhook request source;

## Authentication

The users need to configure a robot account from Harbor side to allow acceld to pull/push images during the conversion process, the following configuration needs to be specified for the robot account：

- Expiration time: `<by user choice>`
- Reset permissions: select `Push Artifact`, `Pull Artifact`, `Create Tag`

## Image Reference

Since the conversion may not rewrite the tag of the original image, we need a way to associate the accelerated image with the original image so that Harbor can:

- Recognize the image format in the portal UI, show a image format icon, expand and collapse the accelerated image items in a hierarchical manner;
- Allow to delete the accelerated image automatically along with the original image;

### Manifest

Accled writes the following fields `subject` into the manifest structure of accelerated image, to allow Harbor to track the relationship between accelerated image and original image:

- `subject` (required): The `subject` field is a descriptor be used to reference the source (original) image, which can be used to avoid duplicate conversion or track the relationship between the source image and converted image in Harbor, it's following the OCI [reference type](https://github.com/opencontainers/image-spec/blob/main/manifest.md#example-image-manifest). 

A complete example with nydus image manifest:

``` json
{
  "schemaVersion": 2,
  "config": {
    "mediaType": "application/vnd.oci.image.config.v1+json",
    "digest": "sha256:563fad1f51cec2ee4c972af4bfd7275914061e2f73770585cfb04309cb5e0d6b",
    "size": 123
  },
  "layers": [
    {
      "mediaType": "application/vnd.oci.image.layer.nydus.blob.v1",
      "digest": "sha256:b6a85be8248b0d3c2f0565ef71d549f404f8edcee1ab666c9871a8e6d9384860",
      "size": 123,
      "annotations": {
        "containerd.io/snapshot/nydus-blob": "true"
      }
    },
    {
      "mediaType": "application/vnd.oci.image.layer.nydus.blob.v1",
      "digest": "sha256:00d151e7d392e68e2c756a6fc42640006ddc0a98d37dba3f90a7b73f63188bbd",
      "size": 123,
      "annotations": {
        "containerd.io/snapshot/nydus-blob": "true"
      }
    },
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
      "digest": "sha256:aec98c9e3dce739877b8f5fe1cddd339de1db2b36c20995d76f6265056dbdb08",
      "size": 123,
      "annotations": {
        "containerd.io/snapshot/nydus-bootstrap": "true"
      }
    }
  ],
  "subject": {
    "mediaType": "application/vnd.oci.image.manifest.v1+json",
    "size": 123,
    "digest": "sha256:5b0bcabd1ed22e9fb1310cf6c2dec7cdef19f0ad69efa1f392e94a4333501270"
  }
}
```

### Manifest Index

According to the latest OCI spec, the `subject` field mentioned above can be defined in manifest or index, but for Harbor, only support mutual traceability between individual manifest.

## Limitation

The following features are not yet supported in the current version of Harbor and should be proposed and implemented:

- Webhook Filter: Harbor should provide some filtering rules to allow users to decide which images can be converted by acceld, such as limiting to a specified repository or tag;
- Webhook Re-schedule: The ability to automatically re-trigger a webhook request or allow the user to manually trigger it if the webhook request fails to be sent (e.g. acceld endpoint is unreachable), or if the image conversion fails on acceld;
