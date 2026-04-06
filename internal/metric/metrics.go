package metric

import "time"

type FailReason string

const (
	FailReasonNoMatchedFlow   FailReason = "no_matched_flow"
	FailReasonBodyTooLarge    FailReason = "body_too_large"
	FailReasonTooManyRequests FailReason = "too_many_requests"
)

type Metrics interface {
	IncRequestsTotal(route, method string, status int)
	UpdateRequestsDuration(route, method string, start time.Time)
	IncRequestsInFlight()
	DecRequestsInFlight()
	IncFailedRequestsTotal(reason FailReason)
	UpdateUpstreamLatency(route, upstream string, start time.Time)
	IncUpstreamRequestsTotal(route, upstream string)
	IncUpstreamErrorsTotal(route, upstream, kind string)
	IncUpstreamRetriesTotal(route, upstream string)
	SetCircuitBreakerState(upstream string, state float64)
}
