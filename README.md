<h1 align="center">Kono API Gateway</h1>

<p align="center">
A lightweight, modular, and high-performance <strong>API Gateway</strong> for modern microservices.
</p>

<p align="center">
Built with simplicity, performance, and developer-friendly configuration in mind.
</p>

[![Go Version](https://img.shields.io/badge/go-1.25.4-blue)](https://golang.org)
[![License](https://img.shields.io/github/license/starwalkn/kono)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/starwalkn/kono)](https://goreportcard.com/report/github.com/starwalkn/kono)
[![codecov](https://codecov.io/gh/starwalkn/kono/branch/master/graph/badge.svg)](https://codecov.io/gh/starwalkn/kono)

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

## 🚀 Quick Start

```bash
git clone https://github.com/starwalkn/kono.git
cd kono

make all GOOS=<YOUR_OS> GOARCH=<YOUR_ARCH>
./bin/kono serve
```

Or with Docker:

```bash
docker run \
  -p 7805:7805 \
  -v $(pwd)/kono.yaml:/app/kono.yaml \
  -e KONO_CONFIG=/app/kono.yaml \
  starwalkn/kono:latest
```

---

## 📖 Documentation

Full documentation, configuration reference, and plugin guide are available at:

**[starwalkn.github.io/konodocs](https://starwalkn.github.io/konodocs/)**

---

## 📄 License

Open-source. See `LICENSE` file for details.

---

<p align="center">
Made with ❤️ in Go
</p>



Show HN: Kono – lightweight API gateway in Go with plugins and Lua scripting

https://starwalkn.github.io/konodocs

Kono is a lightweight API gateway written in Go. The main idea is parallel fan-out — dispatching a single request to multiple upstreams simultaneously and aggregating their responses (merge, array, or namespace by upstream name).
Key things it does out of the box via YAML config:

Fan-out to multiple upstreams with configurable aggregation strategies
Per-upstream circuit breaker, retry with backoff, and load balancing
Request modification via Lua scripts (Lumos) over Unix sockets — no network overhead
Dynamic .so plugins for hooking into request/response phases
Prometheus metrics with circuit breaker state tracking

It's aimed at teams who need a BFF layer or API composition without pulling in a large gateway solution. Still early — v0.1.0, looking for feedback and contributors.
