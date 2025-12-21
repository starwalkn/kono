---
id: introduction
title: Introduction
---

# Tokka API Gateway

**Tokka** is a lightweight and extensible API Gateway written in Go.  
It is designed to simplify request routing, fan-out to multiple upstream services, and response aggregation, while remaining fast, predictable, and easy to configure.

Tokka focuses on **explicit configuration**, **minimal runtime overhead**, and **clear separation of concerns** between routing, middleware, plugins, and upstream policies.

## Key Features

- **High-performance Go core**  
  Built with simplicity and low latency in mind.

- **Multiple upstreams per route**  
  Dispatch requests to several services in parallel and aggregate their responses.

- **Flexible aggregation strategies**  
  Combine upstream responses using merge or array-based aggregation.

- **Pluggable architecture**  
  Extend behavior using dynamically loaded plugins and middlewares.

- **Fine-grained upstream policies**  
  Control retries, timeouts, allowed status codes, body requirements, and response size limits.

- **Metrics support**  
  Built-in metrics integration via VictoriaMetrics.

- **Declarative configuration**  
  Fully configured via a single YAML (or compatible) configuration file.

## Typical Use Cases

- API composition / Backend-for-Frontend (BFF)
- Fan-out and aggregation of microservice responses
- Centralized request validation and transformation
- Edge gateway for internal services
- Lightweight alternative to large API gateway solutions

## Design Philosophy

Tokka aims to be:

- **Small, not bloated** — only core gateway responsibilities
- **Explicit, not magical** — behavior is visible in configuration
- **Composable** — features are built from simple primitives
- **Safe by default** — timeouts, limits, and policies are first-class

Tokka is suitable for both development environments and production deployments where clarity, control, and performance matter.

---
