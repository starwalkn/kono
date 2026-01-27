package metric

import "time"

type FailReason string

const (
	FailReasonGatewayError    FailReason = "gateway_error"
	FailReasonUpstreamError   FailReason = "upstream_error"
	FailReasonNoMatchedRoute  FailReason = "no_matched_route"
	FailReasonPolicyViolation FailReason = "policy_violation"
	FailReasonBodyTooLarge    FailReason = "body_too_large"
	FailReasonUnknown         FailReason = "unknown"
)

type Metrics interface {
	IncRequestsTotal()
	UpdateRequestsDuration(route, method string, start time.Time)
	IncResponsesTotal(int)
	IncRequestsInFlight()
	DecRequestsInFlight()
	IncFailedRequestsTotal(FailReason)
	UpdateUpstreamLatency(route, method, upstream string, lat time.Duration)
}
