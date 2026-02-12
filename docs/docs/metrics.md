---
id: metrics
title: Metrics
---

Kono supports metrics via VictoriaMetrics:

- `/metrics` — endpoint for Prometheus
- Metrics include:
    - `kono_requests_total`
    - `kono_requests_duration`
    - `kono_responses_total{status="..."}`
    - `kono_failed_requests_total{reason="..."}`
    - `kono_requests_in_flight`

Can be connected to Grafana using a VictoriaMetrics datasource.
