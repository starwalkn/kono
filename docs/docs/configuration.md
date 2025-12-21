---
id: configuration
title: Configuration
---

# Tokka Gateway Configuration

This document describes the configuration file format for Tokka Gateway (schema v1), including server settings, routes, upstreams, policies, plugins, and middlewares.

Tokka uses a single declarative configuration file (YAML / JSON / TOML) to define request routing, upstream aggregation, retries, and extensibility.

## Root Configuration

```yaml
schema: v1
name: Tokka Gateway
version: "0.0.1"
debug: false
```

### Fields

| Field     | Type   | Description                                       |
| --------- | ------ | ------------------------------------------------- |
| `schema`  | string | Configuration schema version. Must be `v1`.       |
| `name`    | string | Human-readable gateway name.                      |
| `version` | string | Gateway version (informational).                  |
| `debug`   | bool   | Enables debug logging and additional diagnostics. |

## Server Configuration

```yaml
server:
  port: 7805
  timeout: 5000
  enable_metrics: true
```

### Fields

| Field            | Type | Description                          |
| ---------------- | ---- | ------------------------------------ |
| `port`           | int  | HTTP port the gateway listens on.    |
| `timeout`        | int  | Request timeout in milliseconds.     |
| `enable_metrics` | bool | Enables internal metrics collection. |

## Dashboard Configuration
The dashboard exposes operational and diagnostic endpoints.

```yaml
dashboard:
  enable: true
  port: 7806
  timeout: 5000
```

### Fields

| Field     | Type | Description                                |
| --------- | ---- | ------------------------------------------ |
| `enable`  | bool | Enables the dashboard server.              |
| `port`    | int  | Dashboard HTTP port.                       |
| `timeout` | int  | Dashboard request timeout in milliseconds. |

## Global plugins

```yaml
plugins:
  - name: ratelimit
    config:
      limit: 10
      window: 1
```

### Fields

| Field    | Type              | Description                                         |
| -------- | ----------------- | --------------------------------------------------- |
| `name`   | string            | Plugin name.                                        |
| `path`   | string (optional) | Path to plugin `.so` file (optional for built-ins). |
| `config` | map               | Plugin-specific configuration.                      |

## Global Middlewares
Middlewares wrap HTTP handlers and execute inside the request lifecycle.

```yaml
middlewares:
  - name: recoverer
    path: /tokka/middlewares/recoverer.so
    can_fail_on_load: false
    config:
      enabled: true
```

### Fields

| Field              | Type   | Description                                                          |
| ------------------ | ------ | -------------------------------------------------------------------- |
| `name`             | string | Middleware name.                                                     |
| `path`             | string | Path to middleware `.so` file.                                       |
| `can_fail_on_load` | bool   | Whether gateway startup should continue if middleware fails to load. |
| `override`         | bool   | Overrides global middleware at route level.                          |
| `config`           | map    | Middleware-specific configuration.                                   |

## Routes
Routes define how incoming requests are matched and processed.

```yaml
routes:
  - path: /api/users
    method: GET
    aggregate: merge
    allow_partial_results: true
```

### Route Fields

| Field                   | Type   | Description                                              |
| ----------------------- | ------ | -------------------------------------------------------- |
| `path`                  | string | URL path to match.                                       |
| `method`                | string | HTTP method (GET, POST, PUT, DELETE, etc.).              |
| `middlewares`           | list   | Route-specific middlewares.                              |
| `plugins`               | list   | Route-specific plugins.                                  |
| `upstreams`             | list   | One or more upstream definitions.                        |
| `aggregate`             | string | Aggregation strategy: `merge` or `array`.                |
| `allow_partial_results` | bool   | Allows successful responses even if some upstreams fail. |


## Upstreams
Each route can define multiple upstreams that are executed in parallel.

```yaml
upstreams:
  - url: http://user-service.local/v1/users
    method: GET
    timeout: 3000
```

### Upstream Fields

| Field                   | Type     | Description                                                 |
| ----------------------- | -------- | ----------------------------------------------------------- |
| `url`                   | string   | Target upstream URL.                                        |
| `method`                | string   | HTTP method override (defaults to original request method). |
| `timeout`               | duration | Upstream timeout (e.g. `3000ms`, `1s`).                     |
| `headers`               | map      | Static headers sent to upstream.                            |
| `forward_headers`       | list     | Headers to forward (`*`, `X-*`, or exact names).            |
| `forward_query_strings` | list     | Query params to forward (`*` or specific keys).             |
| `policy`                | object   | Upstream behavior policies.                                 |

## Upstream Policies
Policies control validation, retries, and response handling.

```yaml
policy:
  allowed_statuses: [200, 404]
  require_body: true
  map_status_codes:
    403: 404
  max_response_body_size: 4096
```

### Policy Fields

| Field                    | Type        | Description                               |
| ------------------------ | ----------- | ----------------------------------------- |
| `allowed_statuses`       | list[int]   | List of acceptable HTTP status codes.     |
| `require_body`           | bool        | Fails if upstream response body is empty. |
| `map_status_codes`       | map[int]int | Remaps upstream status codes.             |
| `max_response_body_size` | int         | Maximum response body size in bytes.      |

## Retry Policy

```yaml
retry:
  max_retries: 3
  retry_on_statuses: [500, 502, 503]
  backoff_delay: 1s
```

### Retry Fields

| Field               | Type      | Description                         |
| ------------------- | --------- | ----------------------------------- |
| `max_retries`       | int       | Maximum number of retry attempts.   |
| `retry_on_statuses` | list[int] | HTTP statuses that trigger a retry. |
| `backoff_delay`     | duration  | Delay between retry attempts.       |

## Aggregation Strategies
`merge`
- Expects JSON objects
- Merges keys (later upstreams override earlier ones)

`array`
- Produces a JSON array of upstream responses
- Order is not guaranteed

## Notes & Best Practices

- Prefer `time.Duration` values (`1s`, `500ms`) where supported.
- Use `allow_partial_results: true` for fan-out queries where partial data is acceptable.
- Always set `max_response_body_size` for untrusted upstreams.
- Use wildcard forwarding (`X-*`) carefully to avoid leaking sensitive headers.

## Schema Compatibility
This document applies to:

```yaml
schema: v1
Tokka Gateway >= 0.0.1
```

Future versions may introduce additional fields or deprecate existing ones.


## Example Configuration

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs>
  <TabItem value="yaml" label="YAML" default>
```yaml
schema: v1
name: Tokka Gateway
version: "0.0.1"
debug: false

server:
  port: 7805
  timeout: 5000
  enable_metrics: true

dashboard:
  enable: true
  port: 7806
  timeout: 5000

plugins:
  - name: ratelimit
    config:
      limit: 10
      window: 1

middlewares:
  - name: recoverer
    path: /tokka/middlewares/recoverer.so
    can_fail_on_load: false
    config:
      enabled: true

  - name: logger
    path: /tokka/middlewares/logger.so
    can_fail_on_load: false
    config:
      enabled: true

routes:
  - path: /api/users
    method: GET
    upstreams:
      - url: http://user-service.local/v1/users
        method: GET
        timeout: 3000
        forward_query_strings:
          - "*"
        forward_headers:
          - "X-*"
        policy:
          allowed_statuses: [200, 404]
          require_body: true
          map_status_codes:
            403: 404
          max_response_body_size: 4096
          retry:
            max_retries: 3
            retry_on_statuses: [500, 502, 503]
            backoff_delay: 1s

      - url: http://profile-service.local/v1/details
        method: GET
        forward_headers:
          - "X-Request-ID"
    aggregate: merge
    allow_partial_results: true

  - path: /api/domains
    method: GET
    middlewares:
      - name: logger
        path: /tokka/middlewares/logger.so
        override: true
        config:
          enabled: false
    upstreams:
      - url: http://domain-service.local/v1/domains
        method: GET
        timeout: 3000
        forward_query_strings:
          - "*"
        forward_headers:
          - "X-For*"

      - url: http://profile-service.local/v1/details
        method: GET
        timeout: 2000
        forward_query_strings:
          - "id"
        forward_headers:
          - "X-For"
    aggregate: merge
    allow_partial_results: false

  - path: /api/domains
    method: POST
    middlewares:
      - name: compressor
        path: /tokka/middlewares/compressor.so
        can_fail_on_load: false
        config:
          enabled: true
          alg: gzip
    plugins: []
    upstreams:
      - url: http://domain-service.local/v1/domains
        method: POST
        timeout: 1000

      - url: http://profile-service.local/v1/details
        method: GET
    aggregate: array
    allow_partial_results: false

  - path: /api/domains
    method: DELETE
    middlewares:
      - name: compressor
        path: /tokka/middlewares/compressor.so
        can_fail_on_load: false
        config:
          enabled: true
          alg: deflate

      - name: recoverer
        path: /tokka/middlewares/recoverer.so
        can_fail_on_load: true
        override: true
        config:
          enabled: false
    plugins: []
    upstreams:
      - url: http://domain-service.local/v1/domains
        method: DELETE
        timeout: 2000

      - url: http://profile-service.local/v1/details
        method: GET
    aggregate: array
    allow_partial_results: false
```
  </TabItem>

  <TabItem value="json" label="JSON">
```json
{
  "schema": "v1",
  "name": "Tokka Gateway",
  "version": "0.0.1",
  "debug": false,

  "server": {
    "port": 7805,
    "timeout": 5000,
    "enable_metrics": true
  },

  "dashboard": {
    "enable": true,
    "port": 7806,
    "timeout": 5000
  },

  "plugins": [
    {
      "name": "ratelimit",
      "config": {
        "limit": 10,
        "window": 1
      }
    }
  ],

  "middlewares": [
    {
      "name": "recoverer",
      "path": "/tokka/middlewares/recoverer.so",
      "can_fail_on_load": false,
      "config": {
        "enabled": true
      }
    },
    {
      "name": "logger",
      "path": "/tokka/middlewares/logger.so",
      "can_fail_on_load": false,
      "config": {
        "enabled": true
      }
    }
  ],

  "routes": [
    {
      "path": "/api/users",
      "method": "GET",
      "upstreams": [
        {
          "url": "http://user-service.local/v1/users",
          "method": "GET",
          "timeout": 3000,
          "forward_query_strings": ["*"],
          "forward_headers": ["X-*"],
          "policy": {
            "allowed_statuses": [200, 404],
            "require_body": true,
            "map_status_codes": {
              "403": 404
            },
            "max_response_body_size": 4096,
            "retry": {
              "max_retries": 3,
              "retry_on_statuses": [500, 502, 503],
              "backoff_delay": "1s"
            }
          }
        },
        {
          "url": "http://profile-service.local/v1/details",
          "method": "GET",
          "forward_headers": ["X-Request-ID"]
        }
      ],
      "aggregate": "merge",
      "allow_partial_results": true
    },

    {
      "path": "/api/domains",
      "method": "GET",
      "middlewares": [
        {
          "name": "logger",
          "path": "/tokka/middlewares/logger.so",
          "override": true,
          "config": {
            "enabled": false
          }
        }
      ],
      "upstreams": [
        {
          "url": "http://domain-service.local/v1/domains",
          "method": "GET",
          "timeout": 3000,
          "forward_query_strings": ["*"],
          "forward_headers": ["X-For*"]
        },
        {
          "url": "http://profile-service.local/v1/details",
          "method": "GET",
          "timeout": 2000,
          "forward_query_strings": ["id"],
          "forward_headers": ["X-For"]
        }
      ],
      "aggregate": "merge",
      "allow_partial_results": false
    },

    {
      "path": "/api/domains",
      "method": "POST",
      "middlewares": [
        {
          "name": "compressor",
          "path": "/tokka/middlewares/compressor.so",
          "can_fail_on_load": false,
          "config": {
            "enabled": true,
            "alg": "gzip"
          }
        }
      ],
      "plugins": [],
      "upstreams": [
        {
          "url": "http://domain-service.local/v1/domains",
          "method": "POST",
          "timeout": 1000
        },
        {
          "url": "http://profile-service.local/v1/details",
          "method": "GET"
        }
      ],
      "aggregate": "array",
      "allow_partial_results": false
    },

    {
      "path": "/api/domains",
      "method": "DELETE",
      "middlewares": [
        {
          "name": "compressor",
          "path": "/tokka/middlewares/compressor.so",
          "can_fail_on_load": false,
          "config": {
            "enabled": true,
            "alg": "deflate"
          }
        },
        {
          "name": "recoverer",
          "path": "/tokka/middlewares/recoverer.so",
          "can_fail_on_load": true,
          "override": true,
          "config": {
            "enabled": false
          }
        }
      ],
      "plugins": [],
      "upstreams": [
        {
          "url": "http://domain-service.local/v1/domains",
          "method": "DELETE",
          "timeout": 2000
        },
        {
          "url": "http://profile-service.local/v1/details",
          "method": "GET"
        }
      ],
      "aggregate": "array",
      "allow_partial_results": false
    }
  ]
}
```
  </TabItem>

  <TabItem value="toml" label="TOML">
```toml
schema = "v1"
name = "Tokka Gateway"
version = "0.0.1"
debug = false

[server]
port = 7805
timeout = 5000
enable_metrics = true

[dashboard]
enable = true
port = 7806
timeout = 5000

[[plugins]]
name = "ratelimit"

[plugins.config]
limit = 10
window = 1

[[middlewares]]
name = "recoverer"
path = "/tokka/middlewares/recoverer.so"
can_fail_on_load = false

[middlewares.config]
enabled = true

[[middlewares]]
name = "logger"
path = "/tokka/middlewares/logger.so"
can_fail_on_load = false

[middlewares.config]
enabled = true

[[routes]]
path = "/api/users"
method = "GET"
aggregate = "merge"
allow_partial_results = true

[[routes.upstreams]]
url = "http://user-service.local/v1/users"
method = "GET"
timeout = 3000
forward_query_strings = ["*"]
forward_headers = ["X-*"]

[routes.upstreams.policy]
allowed_statuses = [200, 404]
require_body = true
max_response_body_size = 4096

[routes.upstreams.policy.map_status_codes]
403 = 404

[routes.upstreams.policy.retry]
max_retries = 3
retry_on_statuses = [500, 502, 503]
backoff_delay = "1s"

[[routes.upstreams]]
url = "http://profile-service.local/v1/details"
method = "GET"
forward_headers = ["X-Request-ID"]

[[routes]]
path = "/api/domains"
method = "GET"
aggregate = "merge"
allow_partial_results = false

[[routes.middlewares]]
name = "logger"
path = "/tokka/middlewares/logger.so"
override = true

[routes.middlewares.config]
enabled = false

[[routes.upstreams]]
url = "http://domain-service.local/v1/domains"
method = "GET"
timeout = 3000
forward_query_strings = ["*"]
forward_headers = ["X-For*"]

[[routes.upstreams]]
url = "http://profile-service.local/v1/details"
method = "GET"
timeout = 2000
forward_query_strings = ["id"]
forward_headers = ["X-For"]

[[routes]]
path = "/api/domains"
method = "POST"
aggregate = "array"
allow_partial_results = false
plugins = []

[[routes.middlewares]]
name = "compressor"
path = "/tokka/middlewares/compressor.so"
can_fail_on_load = false

[routes.middlewares.config]
enabled = true
alg = "gzip"

[[routes.upstreams]]
url = "http://domain-service.local/v1/domains"
method = "POST"
timeout = 1000

[[routes.upstreams]]
url = "http://profile-service.local/v1/details"
method = "GET"

[[routes]]
path = "/api/domains"
method = "DELETE"
aggregate = "array"
allow_partial_results = false
plugins = []

[[routes.middlewares]]
name = "compressor"
path = "/tokka/middlewares/compressor.so"
can_fail_on_load = false

[routes.middlewares.config]
enabled = true
alg = "deflate"

[[routes.middlewares]]
name = "recoverer"
path = "/tokka/middlewares/recoverer.so"
can_fail_on_load = true
override = true

[routes.middlewares.config]
enabled = false

[[routes.upstreams]]
url = "http://domain-service.local/v1/domains"
method = "DELETE"
timeout = 2000

[[routes.upstreams]]
url = "http://profile-service.local/v1/details"
method = "GET"
```
  </TabItem>
</Tabs>
