package tokka

import (
	"context"
	"net/http"
	"time"
)

type Upstream interface {
	Name() string
	Policy() UpstreamPolicy
	Call(ctx context.Context, original *http.Request, originalBody []byte, retryPolicy UpstreamRetryPolicy) *UpstreamResponse
}

type UpstreamPolicy struct {
	AllowedStatuses     []int
	RequireBody         bool
	MapStatusCodes      map[int]int
	MaxResponseBodySize int64
	RetryPolicy         UpstreamRetryPolicy
	CircuitBreaker      UpstreamCircuitBreaker
}

type UpstreamRetryPolicy struct {
	MaxRetries      int
	RetryOnStatuses []int
	BackoffDelay    time.Duration
}

type UpstreamCircuitBreaker struct {
	Enabled      bool
	MaxFailures  int
	ResetTimeout time.Duration
}

type UpstreamResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
	Err     *UpstreamError
}

type UpstreamError struct {
	Kind       UpstreamErrorKind // Error kind for aggregator.
	StatusCode int               // Only for bad statuses.
	Err        error             // Original error. Not for client!
}

// Error returns the upstream error kind. Error kind is a string, not error interface!
func (ue *UpstreamError) Error() string {
	return string(ue.Kind)
}

// Unwrap returns the original error.
func (ue *UpstreamError) Unwrap() error {
	return ue.Err
}

type UpstreamErrorKind string

const (
	UpstreamTimeout      UpstreamErrorKind = "timeout"
	UpstreamCanceled     UpstreamErrorKind = "canceled"
	UpstreamConnection   UpstreamErrorKind = "connection"
	UpstreamBadStatus    UpstreamErrorKind = "bad_status"
	UpstreamReadError    UpstreamErrorKind = "read_error"
	UpstreamBodyTooLarge UpstreamErrorKind = "body_too_large"
	UpstreamCircuitOpen  UpstreamErrorKind = "circuit_open"
	UpstreamInternal     UpstreamErrorKind = "internal"
)
