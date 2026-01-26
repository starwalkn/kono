package metric

import (
	"strconv"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

type victoriaMetrics struct {
	RequestsTotal       *metrics.Counter
	RequestsDuration    *metrics.Summary
	ResponsesTotal      map[string]*metrics.Counter
	RequestsInFlight    *metrics.Gauge
	FailedRequestsTotal map[FailReason]*metrics.Counter
}

func NewVictoria() Metrics {
	return &victoriaMetrics{
		RequestsTotal:    metrics.NewCounter("tokka_requests_total"),
		RequestsDuration: metrics.NewSummary("tokka_requests_duration_seconds"),
		ResponsesTotal: map[string]*metrics.Counter{
			"200":   metrics.NewCounter(`tokka_responses_total{status="200"}`),
			"301":   metrics.NewCounter(`tokka_responses_total{status="301"}`),
			"401":   metrics.NewCounter(`tokka_responses_total{status="401"}`),
			"403":   metrics.NewCounter(`tokka_responses_total{status="403"}`),
			"404":   metrics.NewCounter(`tokka_responses_total{status="404"}`),
			"500":   metrics.NewCounter(`tokka_responses_total{status="500"}`),
			"502":   metrics.NewCounter(`tokka_responses_total{status="502"}`),
			"other": metrics.NewCounter(`tokka_responses_total{status="other"}`),
		},
		RequestsInFlight: metrics.NewGauge(`tokka_requests_in_flight`, nil),
		FailedRequestsTotal: map[FailReason]*metrics.Counter{
			FailReasonGatewayError:    metrics.NewCounter(`tokka_failed_requests_total{reason="gateway_error"}`),
			FailReasonUpstreamError:   metrics.NewCounter(`tokka_failed_requests_total{reason="upstream_error"}`),
			FailReasonNoMatchedRoute:  metrics.NewCounter(`tokka_failed_requests_total{reason="no_matched_route"}`),
			FailReasonBodyTooLarge:    metrics.NewCounter(`tokka_failed_requests_total{reason="body_too_large"}`),
			FailReasonPolicyViolation: metrics.NewCounter(`tokka_failed_requests_total{reason="policy_violation"}`),
			FailReasonUnknown:         metrics.NewCounter(`tokka_failed_requests_total{reason="unknown"}`),
		},
	}
}

func (m *victoriaMetrics) IncRequestsTotal() {
	m.RequestsTotal.Inc()
}

func (m *victoriaMetrics) UpdateRequestsDuration(start time.Time) {
	m.RequestsDuration.UpdateDuration(start)
}

func (m *victoriaMetrics) IncResponsesTotal(status int) {
	if _, ok := m.ResponsesTotal[strconv.Itoa(status)]; !ok {
		m.ResponsesTotal["other"].Inc()
		return
	}

	m.ResponsesTotal[strconv.Itoa(status)].Inc()
}

func (m *victoriaMetrics) IncRequestsInFlight() {
	m.RequestsInFlight.Inc()
}

func (m *victoriaMetrics) DecRequestsInFlight() {
	m.RequestsInFlight.Dec()
}

func (m *victoriaMetrics) IncFailedRequestsTotal(reason FailReason) {
	if _, ok := m.FailedRequestsTotal[reason]; !ok {
		m.FailedRequestsTotal[FailReasonUnknown].Inc()
		return
	}

	m.FailedRequestsTotal[reason].Inc()
}
