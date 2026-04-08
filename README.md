<h1 align="center">Kono API Gateway</h1>

<p align="center">
A lightweight, modular, and high-performance <strong>API Gateway</strong> for modern microservices.
</p>

<p align="center">
Built with simplicity, performance, and developer-friendly configuration in mind.
</p>

<p align="center">
  <a href="https://golang.org">
    <img src="https://img.shields.io/badge/go-1.25.4-blue" alt="Go Version"/>
  </a>
  <a href="LICENSE">
    <img src="https://img.shields.io/github/license/starwalkn/kono" alt="License"/>
  </a>
  <a href="https://goreportcard.com/report/github.com/starwalkn/kono">
    <img src="https://goreportcard.com/badge/github.com/starwalkn/kono" alt="Go Report Card"/>
  </a>
  <a href="https://codecov.io/gh/starwalkn/kono">
    <img src="https://codecov.io/gh/starwalkn/kono/branch/master/graph/badge.svg" alt="codecov"/>
  </a>
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
