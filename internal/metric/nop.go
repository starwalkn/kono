package metric

import (
	"time"

	"go.uber.org/zap"
)

type nopMetrics struct{}

func NewNop() Metrics {
	return &nopMetrics{}
}

func (m *nopMetrics) IncRequestsTotal()                   {}
func (m *nopMetrics) UpdateRequestsDuration(_ time.Time)  {}
func (m *nopMetrics) IncResponsesTotal(_ int)             {}
func (m *nopMetrics) IncRequestsInFlight()                {}
func (m *nopMetrics) DecRequestsInFlight()                {}
func (m *nopMetrics) IncFailedRequestsTotal(_ FailReason) {}
func (m *nopMetrics) IncCounter(_ string, _ ...zap.Field) {}
