# Changelog

All notable changes to Kono are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/).

---

## [0.2.0] — 2026-04-26

### Added

- **Passthrough mode** — new `passthrough: true` flow option proxies requests directly to a single upstream without buffering or aggregation. Designed for Server-Sent Events (SSE), chunked transfer, and any long-lived HTTP connection. Request plugins still run; response plugins are skipped.
- **OTLP metrics exporter** — metrics can now be pushed to any OpenTelemetry-compatible backend via `exporter: otlp`. Previously only Prometheus pull-mode was supported.
- **Namespace aggregation strategy** — new `strategy: namespace` places each upstream response under its name as a key: `{"profile": {...}, "stats": {...}}`.
- **Response meta envelope** — all gateway responses now include a `meta` object with `request_id` (ULID) and `partial` flag alongside `data` and `errors`.
- **`kono viz` command** — CLI command that renders a visual tree of all configured flows and upstreams using terminal color output.
- **JWKS background refresh** — the `auth` middleware now refreshes JWKS keys in the background on a configurable interval (`jwks_refresh_interval`, default 5m), reducing on-demand refresh latency during key rotation.
- **`sdk.Closer` interface** — middlewares that hold background resources can implement `Close() error`. Kono calls it on shutdown via `Router.Close()`.
- **Configuration defaults via struct tags** — upstream timeouts, transport pool settings, and server timeout now use `default:` tags powered by `creasty/defaults`, eliminating the manual `ensureGatewayDefaults` function.
- **`kono validate` command** — validates the configuration file and reports human-readable field-level errors without starting the gateway.
- **Full hop-by-hop header filtering** — `Keep-Alive`, `TE`, `Proxy-Authenticate`, `Proxy-Authorization`, and `Upgrade` are now stripped from proxied responses in addition to the previously filtered headers.
- **`cors` built-in middleware** — Cross-Origin Resource Sharing support with configurable origins, methods, headers, credentials, and preflight cache.
- **`ClientErrAborted` error code** — new `ABORTED` error code and `503 Service Unavailable` status for requests cancelled by the client before completion.

### Changed

- **`dispatcher` renamed to `scatter`** — internal component renamed to better reflect the fan-out pattern. No user-facing configuration change.
- **Upstream policy validation moved into upstream** — `requireBody` and `allowedStatuses` policy checks now run inside `upstream.call()` after the circuit breaker update, so policy violations do not affect circuit breaker state.
- **`aggregation` is now optional for passthrough flows** — the `aggregation` block may be omitted when `passthrough: true`. Configuration validation enforces this.
- **`pprof.port` only required when pprof is enabled** — previously the validator required a port value unconditionally.
- **`AggregationConfig` is now a pointer in `FlowConfig`** — allows the validator to correctly apply `required_if=Passthrough false`.
- **`Router.flows` and all internal types unexported** — `Flow`, `Upstream`, `AggregatedResponse`, `UpstreamResponse`, `UpstreamError`, and related types are no longer exported. Public API is limited to `Router`, `NewRouter`, `RoutingConfigSet`, config types, `LoadConfig`, `ClientError`, `ClientResponse`, and `WriteError`.

### Fixed

- `passthrough` field not being set on compiled `flow` struct — passthrough flows were silently falling back to buffered mode.
- `trackingWriter` not forwarding `Flush()` — SSE events were buffered until connection close instead of being flushed after each chunk.
- `headersAlreadySent` always returning `true` — used `http.Flusher` type assertion which is satisfied by almost any `ResponseWriter`, making the double-write guard ineffective.
- `Content-Length` forwarded from upstream in passthrough mode — conflicted with chunked/streaming responses and caused client parsing errors.
- Circuit breaker state not updated correctly on `HalfOpen` success and `Open` failure — missing `case` branches in switch statements.

---

## [0.1.0] — initial release

- Core routing with chi
- Fan-out dispatch to multiple upstreams
- `merge` and `array` aggregation strategies
- Circuit breaker, retry, load balancing per upstream
- Prometheus metrics
- Plugin and middleware `.so` loading
- Built-in plugins: `camelify`, `snakeify`, `masker`
- Built-in middlewares: `auth`, `logger`, `recoverer`, `compressor`
- `trusted_proxies` and per-IP rate limiting
- YAML configuration with validation