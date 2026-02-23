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

Kono requires a YAML configuration file.

It looks for configuration in:

1. `KONO_CONFIG` environment variable
2. `./kono.json` (fallback)

### Example Configuration (v1)

```yaml
config_version: v1
name: Kono Gateway
version: 0.0.1
debug: false

server:
  port: 7805
  timeout: 20s
  metrics:
    enabled: true
    provider: prometheus

middlewares:
  - name: recoverer
    path: /kono/middlewares/recoverer.so
    can_fail_on_load: false
    config:
      enabled: true

  - name: logger
    path: /kono/middlewares/logger.so
    can_fail_on_load: false
    config:
      enabled: true

router:
  rate_limiter:
    enabled: true
    config:
      limit: 10
      window: 1s

  routes:
    - path: /api/users
      method: GET
      aggregation:
        strategy: merge
        allow_partial_results: true
      max_parallel_upstreams: 1
      upstreams:
        - hosts:
            - http://user-service.local/v1/users
          method: GET
          timeout: 3s
          forward_query_strings: [ "*" ]
          forward_headers: [ "X-*" ]
          policy:
            allowed_statuses: [ 200, 404 ]
            retry:
              max_retries: 3
              retry_on_statuses: [ 500, 502, 503 ]
              backoff_delay: 1s
            circuit_breaker:
              enabled: true
              max_failures: 5
              reset_timeout: 2s
      plugins:
        - name: snakeify
          path: /kono/plugins/snakeify.so

    - path: /api/domains
      method: GET
      aggregation:
        strategy: array
        allow_partial_results: true
      max_parallel_upstreams: 3
      upstreams:
        - hosts:
            - http://domain-service.local/v1/domains
          method: GET
          timeout: 3s
          forward_query_strings: [ "*" ]
          forward_headers: [ "X-For*" ]
          policy:
            circuit_breaker:
              enabled: true
              max_failures: 5
              reset_timeout: 2s
      plugins:
        - name: snakeify
          path: /kono/plugins/snakeify.so
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