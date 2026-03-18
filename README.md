<h1 align="center">Kono API Gateway</h1>

<p align="center">
A lightweight, modular, and high-performance <strong>API Gateway</strong> for modern microservices.
</p>

<p align="center">
Kono Gateway provides advanced routing, request fan-out, response aggregation,
pluggable middleware, and extensibility through custom <code>.so</code> plugins.
</p>

<p align="center">
Built with simplicity, performance, and developer-friendly configuration in mind.
</p>

---

## ✨ Features

- 🚀 High-performance HTTP reverse proxy
- 🔀 Request fan-out & response aggregation
- 🧩 Dynamic `.so` plugin system
- 🛠 Request & response mutation
- 🔁 Retry, circuit breaker & status mapping
- 📊 Metrics support (Prometheus-compatible)
- 📦 YAML-based configuration
- 🐳 Docker-ready

---

## 📦 Installation (Local Build)

```bash
git clone https://github.com/starwalkn/kono.git
cd kono

make all GOOS=<YOUR_OS> GOARCH=<YOUR_ARCH>

./bin/kono serve
```

Available CLI commands:

```bash
kono serve
kono validate
kono viz
```

Instead of defining the `KONO_CONFIG` environment variable, you can also explicitly 
pass the path to the configuration file using the `--config` flag:

```bash
kono --config /etc/kono/config.yaml serve
```

---

## 🐳 Run with Docker

```bash
docker build -f build/Dockerfile -t kono:local .

docker run \
  -p 7805:7805 \
  -v $(pwd)/kono.yaml:/app/kono.yaml \
  -e KONO_CONFIG=/app/kono.yaml \
  kono:local
```

---

## ⚙️ Configuration

⚠️ Kono only supports YAML configuration files. JSON/TOML is not supported to avoid inconsistencies and reduce
complexity.

It looks for configuration in:

1. `KONO_CONFIG` environment variable
2. `/etc/kono/config.yaml` (if `KONO_CONFIG` is empty)

### Example Configuration (v1)

```yaml
schema: v1
debug: true

gateway:
  server:
    port: 7805
    timeout: 20s
    metrics:
      enabled: true
      provider: prometheus

  routing:
    rate_limiter:
      enabled: true
      config:
        limit: 10
        window: 1s
    flows:
      - path: /api/users
        method: GET
        aggregation:
          strategy: merge
          allow_partial_results: true
        max_parallel_upstreams: 1
        plugins:
          - name: snakeify
            path: /kono/plugins/snakeify.so
        middlewares:
          - name: recoverer
            path: /kono/middlewares/recoverer.so
            config:
              enabled: false
          - name: logger
            path: /kono/middlewares/logger.so
            config:
              enabled: false
        upstreams:
          - hosts: http://user-service.local
            path: v1/users
            method: GET
            timeout: 3s
            forward_queries: [ "*" ]
            forward_headers: [ "X-*" ]
            policy:
              allowed_statuses: [ 200, 404 ]
              require_body: true
              map_status_codes:
                403: 404
              max_response_body_size: 4096
              retry:
                max_retries: 3
                retry_on_statuses: [ 500, 502, 503 ]
                backoff_delay: 1s
              circuit_breaker:
                enabled: true
                max_failures: 5
                reset_timeout: 2s

      - path: /api/domains
        method: GET
        aggregation:
          strategy: array
          allow_partial_results: true
        max_parallel_upstreams: 3
        plugins:
          - name: camelify
            path: /kono/plugins/camelify.so
        middlewares:
          - name: logger
            path: /kono/middlewares/logger.so
            config:
              enabled: false
        upstreams:
          - hosts:
              - http://domain-service-1.local
              - http://domain-service-2.local
            path: v1/domains
            method: GET
            timeout: 3s
            forward_queries: [ "*" ]
            forward_headers: [ "X-For*" ]
            policy:
              circuit_breaker:
                enabled: true
                max_failures: 5
                reset_timeout: 2s
              load_balancing:
                mode: round_robin
          - hosts:
              - http://profile-service-1.local
              - http://profile-service-2.local
              - http://profile-service-3.local
            path: v1/details
            method: GET
            timeout: 2s
            forward_queries: [ "id" ]
            forward_headers: [ "X-For" ]
            policy:
              circuit_breaker:
                enabled: true
                max_failures: 5
                reset_timeout: 2s
              load_balancing:
                mode: least_conns

```

---

## 🔌 Plugins

Kono supports dynamic Go plugins compiled as `.so`:

```bash
CGO_ENABLED=1 go build -buildmode=plugin -o myplugin.so ./plugins/myplugin
```

Plugins can:

- Modify `*http.Request`
- Modify aggregated `*http.Response`
- Inject headers
- Validate requests
- Short-circuit responses

> ⚠️ Plugins must be compiled with the exact same Go version as the gateway binary.

---

## 🧪 Validate Configuration

```bash
kono validate
```

Or inside Docker:

```bash
docker run \
  -v $(pwd)/kono.yaml:/app/kono.yaml \
  -e KONO_CONFIG=/app/kono.yaml \
  kono:local validate
```

---

## 🛠 Development

See `CONTRIBUTING.md` for development workflow and plugin guidelines.

---

## 📄 License

Open-source. See `LICENSE` file for details.

---

<p align="center">
Made with ❤️ in Go
</p>