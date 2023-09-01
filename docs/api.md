# API

Acceld acts as an HTTP server to serve webhook HTTP requests
from Harbor for image conversions, acceld exposes following APIs:

- [Create Task](#create-task)
- [List Task](#list-task)
- [Check Healthy](#check-healthy)

---
<a name="create-task"></a>

## Create Task

### Request

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

### Response

```
Ok
```

| Status | Description                                  |
| ------ | -------------------------------------------- |
| 200    | Task created/Task finished                   |
| 400    | Illegal parameter                            |
| 401    | Unauthorized, invalid `Authorization` header |

<a name="list-task"></a>

## List Task

### Request

```
GET /api/v1/conversions
```

### Response

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

## Check Healthy

### Request

```
GET /api/v1/health
```

### Response

```
Ok
```

| Status | Description                 |
| ------ | --------------------------- |
| 200    | Accled service is healthy   |
| 500    | Accled service is unhealthy |