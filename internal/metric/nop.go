package metric

import (
	"time"
)

type nopMetrics struct{}

func NewNop() Metrics {
	return &nopMetrics{}
}
func (m *nopMetrics) IncRequestsTotal()                                     {}
func (m *nopMetrics) UpdateRequestsDuration(_, _ string, _ time.Time)       {}
func (m *nopMetrics) IncResponsesTotal(_ string, _ int)                     {}
func (m *nopMetrics) IncRequestsInFlight()                                  {}
func (m *nopMetrics) DecRequestsInFlight()                                  {}
func (m *nopMetrics) IncFailedRequestsTotal(_ FailReason)                   {}
func (m *nopMetrics) UpdateUpstreamLatency(_, _, _ string, _ time.Duration) {}
