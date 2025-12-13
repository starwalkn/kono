# 🚀 Tokka Gateway
A lightweight, modular, and high-performance **API Gateway** for modern microservices.

Tokka Gateway provides advanced routing, request fan-out, response aggregation, pluggable middleware, and extensibility through custom `.so` modules.  
It is built with simplicity, performance, and developer-friendly configuration in mind.

---

## ✨ Features

- **Flexible routing** with support for multiple backend services
- **Response aggregation** (`merge`, `array`)
- **Global and per-route middleware**
- **Plugin system** (e.g., rate limiting)
- **Custom extensions using `.so` modules**
- **Parallel request execution**
- **Optional partial aggregation (`allow_partial_results`)**
- **Built-in dashboard**
- **Declarative YAML configuration**

---

## 📦 Example Configuration

```yaml
schema: v1
name: Tokka Gateway
version: "0.0.1-beta"
debug: false

server:
  port: 8090
  timeout: 5000

dashboard:
  enable: true
  port: 8095
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
    backends:
      - url: http://user-service.local/v1/users
        method: GET
        timeout: 3000
        forward_query_strings: ["*"]
        forward_headers: ["X-*"]

      - url: http://profile-service.local/v1/details
        method: GET
        forward_headers: ["X-Request-ID"]
    aggregate: merge
    transform: default
    allow_partial_results: true

  - path: /api/domains
    method: GET
    middlewares:
      - name: logger
        path: /tokka/middlewares/logger.so
        override: true
        config:
          enabled: false
    backends:
      - url: http://domain-service.local/v1/domains
        method: GET
        timeout: 3000
        forward_query_strings: ["*"]
        forward_headers: ["X-For*"]

      - url: http://profile-service.local/v1/details
        method: GET
        timeout: 2000
        forward_query_strings: ["id"]
        forward_headers: ["X-For"]
    aggregate: merge
    transform: default
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
    backends:
      - url: http://domain-service.local/v1/domains
        method: POST
        timeout: 1000

      - url: http://profile-service.local/v1/details
        method: GET
    aggregate: array
    transform: default
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
    backends:
      - url: http://domain-service.local/v1/domains
        method: DELETE
        timeout: 2000

      - url: http://profile-service.local/v1/details
        method: GET
    aggregate: array
    transform: default
    allow_partial_results: false
```

---

## 🔌 Plugins & Middleware

Tokka Gateway supports dynamically loaded **shared objects (`.so`)** for plugins and middleware.

### Global Middleware
Defined under the top-level `middlewares:` block; applied to all routes.

### Per-Route Middleware
Routes may **override** global middleware or add new ones.

### Custom Plugins / Middleware

Example build:

```bash
CGO_ENABLED=1 go build -buildmode=plugin -o /plugin/out.so /path/to/plugin/code.go
```

---

## 🧩 Aggregation Modes

### `merge`
Merges objects from each backend into a single result.

### `array`
Returns ordered list of backend responses.

### Partial Results

```yaml
allow_partial_results: true|false
```

---

## 📊 Dashboard

Enable the built-in dashboard:

```yaml
dashboard:
  enable: true
  port: 8095
```

---

## 🧭 Roadmap

- Hot-reload support for plugins
- Built-in JWT authentication middleware
- Lua-based scriptable transforms
- gRPC backend support
- Prometheus metrics exporter
- Real-time web UI for route statistics

---

## 🤝 Contributing

Contributions are welcome!  
Please open issues and pull requests to help improve the project.

---

## 📜 License

Released under the **MIT License**.
