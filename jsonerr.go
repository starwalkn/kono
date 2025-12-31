package tokka

type JsonError struct {
	Code      string
	Upstream  string
	RequestID string
}

const (
	ErrorRateLimitExceeded = "RATE_LIMIT_EXCEEDED"
	ErrorPayloadTooLarge   = "PAYLOAD_TOO_LARGE"
	ErrorCodeInternal      = "INTERNAL"
)

const (
	jsonErrRateLimitExceeded = `{"error":"rate limit exceeded"}`
	jsonErrPayloadTooLarge   = `{"error":"payload too large"}`
	jsonErrInternal          = `{"error":"internal"}`
)
