package metric

import (
	"time"
)

type nopMetrics struct{}

func NewNop() Metrics {
	return &nopMetrics{}
}

func (n nopMetrics) IncRequestsTotal(_, _ string, _ int)             {}
func (n nopMetrics) UpdateRequestsDuration(_, _ string, _ time.Time) {}
func (n nopMetrics) IncRequestsInFlight()                            {}
func (n nopMetrics) DecRequestsInFlight()                            {}
func (n nopMetrics) IncFailedRequestsTotal(_ FailReason)             {}
func (n nopMetrics) UpdateUpstreamLatency(_, _ string, _ time.Time)  {}
func (n nopMetrics) IncUpstreamRequestsTotal(_, _ string)            {}
func (n nopMetrics) IncUpstreamErrorsTotal(_, _, _ string)           {}
func (n nopMetrics) IncUpstreamRetriesTotal(_, _ string)             {}
func (n nopMetrics) SetCircuitBreakerState(_ string, _ float64)      {}
