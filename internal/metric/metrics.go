package metric

import "time"

type FailReason string

const (
	FailReasonGatewayError    FailReason = "gateway_error"
	FailReasonUpstreamError   FailReason = "upstream_error"
	FailReasonNoMatchedRoute  FailReason = "no_matched_route"
	FailReasonPolicyViolation FailReason = "policy_violation"
	FailReasonBodyTooLarge    FailReason = "body_too_large"
)

type Metrics interface {
	IncRequestsTotal()
	UpdateRequestsDuration(time.Time)
	IncResponsesTotal(int)
	IncRequestsInFlight()
	DecRequestsInFlight()
	IncFailedRequestsTotal(FailReason)
}
