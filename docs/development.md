# Development

## API

Acceld acts as an HTTP server to serve webhook HTTP requests
from Harbor for image conversions, acceld exposes following APIs:

- [Create Task](#create-task)
- [List Task](#list-task)
- [Check Healthy](#check-healthy)

---
<a name="create-task"></a>

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

<a name="list-task"></a>

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

<a name="check-healthy"></a>

### Check Healthy

#### Request

```
GET /api/v1/health
```

#### Response

```
Ok
```

| Status | Description                 |
| ------ | --------------------------- |
| 200    | Accled service is healthy   |
| 500    | Accled service is unhealthy |

## Driver

### Interface

Acceleration Service Framework provides a built-in extensible method called driver, which allows the integration of various types of accelerated image format conversions, the framework will automatically handle operations such as pulling source image and pushing converted image, the format providers need to implement the following interface in [pkg/driver](../pkg/driver):

```golang
// Driver defines image conversion interface, the following
// methods need to be implemented by image format providers.
type Driver interface {
	// Convert converts the source image to target image, where
	// content parameter provides necessary image utils, image
	// content store and so on, where source parameter is the
	// original image reference. If conversion successful, the
	// converted image manifest will be returned, otherwise a
	// non-nil error will be returned.
	Convert(ctx context.Context, content content.Provider, source string) (*ocispec.Descriptor, error)

	// Name gets the driver type name, it is used to identify
	// different accelerated image formats.
	Name() string

	// Version gets the driver version, it is used to identify
	// different accelerated image format versions with same driver.
	Version() string
}
```

### Testing

We can specify the driver name by modifying `converter.driver` in the configuration file, and modify the fields in `converter.config` to specify the driver-related configuration, see [example configuration file](../misc/config/config.estargz.yaml).
