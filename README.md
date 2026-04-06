<h1 align="center">Kono API Gateway</h1>

<p align="center">
A lightweight, modular, and high-performance <strong>API Gateway</strong> for modern microservices.
</p>

<p align="center">
Kono provides advanced routing, request fan-out, response aggregation,
pluggable middleware, Lua scripting, and extensibility through custom <code>.so</code> plugins.
</p>

<p align="center">
Built with simplicity, performance, and developer-friendly configuration in mind.
</p>

---

## ✨ Features

- 🚀 High-performance HTTP reverse proxy
- 🔀 Request fan-out & response aggregation (merge, array, namespace)
- 🧩 Dynamic `.so` plugin system (request & response phase)
- 📜 Lua scripting via **Lumos** (LuaJIT over Unix socket)
- 🔗 Path parameter extraction and forwarding
- 🔁 Retry, circuit breaker & load balancing (round-robin, least-conns)
- 📊 Prometheus metrics with circuit breaker state tracking
- 🛡 Rate limiting & trusted proxy support
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
kono serve      # start the gateway
kono validate   # validate configuration file
kono viz        # visualize flow configuration
```

Instead of the `KONO_CONFIG` environment variable, you can pass the config path explicitly:
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

⚠️ Kono only supports YAML. JSON/TOML is not supported.

Config is resolved in this order:

1. `--config` flag
2. `KONO_CONFIG` environment variable
3. `/etc/kono/config.yaml`

### Full Example (v1)
```yaml
schema: v1
debug: true

gateway:
  server:
    port: 7805
    timeout: 20s
    pprof:
      enabled: true
      port: 6060
    metrics:
      enabled: true
      provider: prometheus

  routing:
    trusted_proxies:
      - 127.0.0.1/32
      - 10.0.0.0/8
    rate_limiter:
      enabled: true
      config:
        limit: 100
        window: 1s

    flows:
      - path: /api/v1/users/{user_id}/orders/{order_id}
        method: GET
        aggregation:
          best_effort: true
          strategy: namespace
        max_parallel_upstreams: 10
        plugins:
          - name: snakeify
            source: builtin
        middlewares:
          - name: recoverer
            source: builtin
          - name: logger
            source: builtin
            config:
              enabled: true
        scripts:
          - source: file
            path: /etc/kono/scripts/auth.lua
        upstreams:
          - name: users
            hosts: http://user-service.local
            path: /v1/users/{user_id}
            method: GET
            timeout: 3s
            forward_queries: ["*"]
            forward_headers: ["X-*"]
            policy:
              allowed_statuses: [200, 404]
              require_body: true
              max_response_body_size: 4096
              header_blacklist: ["X-Internal-Token"]
              retry:
                max_retries: 3
                retry_on_statuses: [500, 502, 503]
                backoff_delay: 500ms
              circuit_breaker:
                enabled: true
                max_failures: 5
                reset_timeout: 2s

          - name: orders
            hosts:
              - http://orders-service-1.local
              - http://orders-service-2.local
            path: /v1/orders/{order_id}
            method: GET
            timeout: 3s
            forward_queries: ["*"]
            forward_headers: ["X-*"]
            forward_params: ["user_id"]   # forwarded as ?user_id=... to upstream
            policy:
              allowed_statuses: [200, 404]
              require_body: true
              retry:
                max_retries: 2
                retry_on_statuses: [500, 503]
                backoff_delay: 250ms
              circuit_breaker:
                enabled: true
                max_failures: 5
                reset_timeout: 2s
              load_balancing:
                mode: round_robin   # round_robin | least_conns
```

---

## 🔀 Aggregation Strategies

| Strategy | Description |
|---|---|
| `merge` | Merges JSON objects from all upstreams into one. Supports conflict policies: `overwrite`, `first`, `error`, `prefer`. |
| `array` | Wraps all upstream responses in a JSON array. |
| `namespace` | Places each upstream response under its name as a key: `{"users": {...}, "orders": {...}}`. |

`best_effort: true` allows returning partial results when some upstreams fail (HTTP 206).

---

## 🔗 Path Parameters

Kono supports path parameters in flow and upstream paths:
```yaml
flows:
  - path: /api/v1/transactions/{transaction_id}
    upstreams:
      - name: transactions
        path: /transactions/{transaction_id}   # expanded automatically
        forward_params: ["transaction_id"]     # also forwarded as query param
```

Parameters are extracted by the router and available to all upstreams in the flow.

---

## 🔌 Plugins

Plugins are compiled as Go `.so` files and loaded at startup:
```bash
CGO_ENABLED=1 go build -buildmode=plugin -o myplugin.so ./plugins/myplugin
```

Two plugin phases are supported:

- **Request phase** — runs before upstream dispatch. Can modify request context.
- **Response phase** — runs after aggregation. Can modify response headers and body.

Plugins can be loaded from builtin paths or custom file paths:
```yaml
plugins:
  - name: myplugin
    source: file
    path: /etc/kono/plugins/
```

> ⚠️ Plugins must be compiled with the exact same Go version as the gateway binary.

---

## 📜 Lumos (Lua Scripting)

Lumos allows request modification via LuaJIT scripts running as sidecar processes in the same container, communicating with Kono over Unix sockets — no extra network overhead.
```yaml
scripts:
  - source: file
    path: /etc/kono/scripts/auth.lua
```

A Lumos script returns one of two actions:

- `continue` — proceed with (optionally modified) request
- `abort` — reject the request with a specific HTTP status

---

## 📊 Metrics

When `metrics.provider: prometheus` is enabled, metrics are available at `/metrics`:

| Metric | Description |
|---|---|
| `kono_requests_total` | Total requests by route, method, status |
| `kono_requests_duration_seconds` | End-to-end request latency |
| `kono_requests_in_flight` | Current in-flight requests |
| `kono_failed_requests_total` | Rejected requests by reason |
| `kono_upstream_requests_total` | Requests dispatched to upstreams |
| `kono_upstream_errors_total` | Upstream errors by kind |
| `kono_upstream_latency_seconds` | Upstream response latency |
| `kono_upstream_retries_total` | Retry attempts per upstream |
| `kono_circuit_breaker_state` | Circuit breaker state: 0=closed, 1=open, 2=half-open |

---

## 🏥 Health Check

`GET /__health`

Returns `200 OK` when the gateway is running. The `__` prefix avoids conflicts with user-defined flow paths.

---

## 🔍 Profiling

When `pprof.enabled: true`, profiling endpoints are available at `localhost:<pprof.port>/debug/pprof/`.

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