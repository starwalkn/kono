---
id: metrics
title: Metrics
---

Tokka supports metrics via VictoriaMetrics:

- `/metrics` — endpoint for Prometheus
- Metrics include:
  - `tokka_requests_total`
  - `tokka_requests_duration`
  - `tokka_responses_total{status="..."}`
  - `tokka_failed_requests_total{reason="..."}`
  - `tokka_requests_in_flight`
  
Can be connected to Grafana using a VictoriaMetrics datasource.
