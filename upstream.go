package kono

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/xff16/kono/internal/circuitbreaker"
)

type Upstream interface {
	Name() string
	Policy() Policy
	Call(ctx context.Context, original *http.Request, originalBody []byte) *UpstreamResponse
}

type UpstreamResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
	Err     *UpstreamError
}

type UpstreamError struct {
	Kind UpstreamErrorKind // Error kind for aggregator.
	Err  error             // Original error. Not for client!
}

// Error returns the upstream error kind. Error kind is a custom string type, not error interface!
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

// httpUpstream is an implementation of Upstream interface.
type httpUpstream struct {
	id                  string // UUID for internal usage.
	name                string // For logs.
	hosts               []string
	currentHostIdx      uint64
	method              string
	timeout             time.Duration
	forwardHeaders      []string
	forwardQueryStrings []string
	policy              Policy

	circuitBreaker *circuitbreaker.CircuitBreaker

	log    *zap.Logger
	client *http.Client
}

func (u *httpUpstream) Name() string   { return u.name }
func (u *httpUpstream) Policy() Policy { return u.policy }

func (u *httpUpstream) Call(ctx context.Context, original *http.Request, originalBody []byte) *UpstreamResponse {
	log := u.log.With(zap.String("upstream", u.name))

	resp := &UpstreamResponse{}

	retryPolicy := u.policy.RetryPolicy

	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			resp.Err = &UpstreamError{
				Kind: UpstreamCanceled,
				Err:  ctx.Err(),
			}

			return resp
		default:
			if u.circuitBreaker != nil {
				if allow := u.circuitBreaker.Allow(); !allow {
					log.Error("circuit breaker deny request")

					return &UpstreamResponse{
						Err: &UpstreamError{
							Kind: UpstreamCircuitOpen,
							Err:  errors.New("upstream circuit breaker is open"),
						},
					}
				}
			}

			resp = u.call(ctx, original, originalBody, log)

			if u.circuitBreaker != nil {
				if resp.Err != nil && u.isBreakerFailure(resp.Err) {
					log.Error("upstream request failed, opening circuit breaker")
					u.circuitBreaker.OnFailure()
				} else {
					u.circuitBreaker.OnSuccess()
				}
			}

			if resp.Err == nil && !slices.Contains(retryPolicy.RetryOnStatuses, resp.Status) {
				break
			}

			if retryPolicy.BackoffDelay > 0 {
				select {
				case <-time.After(retryPolicy.BackoffDelay):
				case <-ctx.Done():
					resp.Err = &UpstreamError{
						Kind: UpstreamCanceled,
						Err:  ctx.Err(),
					}

					return resp
				}
			}
		}
	}

	return resp
}

func (u *httpUpstream) call(ctx context.Context, original *http.Request, originalBody []byte, log *zap.Logger) *UpstreamResponse {
	uresp := &UpstreamResponse{
		Headers: make(http.Header),
	}

	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	req, err := u.newRequest(ctx, original, originalBody)
	if err != nil {
		uresp.Err = &UpstreamError{
			Kind: UpstreamInternal,
			Err:  err,
		}

		return uresp
	}

	hresp, err := u.client.Do(req)
	if err != nil {
		log.Error("non-successful upstream request", zap.Error(err))

		kind := UpstreamConnection

		if errors.Is(err, context.DeadlineExceeded) {
			kind = UpstreamTimeout
		}

		if errors.Is(err, context.Canceled) {
			kind = UpstreamCanceled
		}

		uresp.Err = &UpstreamError{
			Kind: kind,
			Err:  err,
		}

		return uresp
	}
	defer hresp.Body.Close()

	uresp.Status = hresp.StatusCode

	if hresp.StatusCode >= http.StatusInternalServerError {
		log.Error("non-200 upstream response status code", zap.Int("status_code", hresp.StatusCode))

		uresp.Err = &UpstreamError{
			Kind: UpstreamBadStatus,
			Err:  errors.New("upstream error"),
		}

		return uresp
	}

	uresp.Headers = hresp.Header.Clone()

	var reader io.Reader = hresp.Body
	if u.policy.MaxResponseBodySize > 0 {
		log.Debug("using limit reader", zap.Int64("max_response_body_size", u.policy.MaxResponseBodySize))
		reader = io.LimitReader(hresp.Body, u.policy.MaxResponseBodySize+1)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		uresp.Err = &UpstreamError{
			Kind: UpstreamReadError,
			Err:  err,
		}

		return uresp
	}

	if u.policy.MaxResponseBodySize > 0 && int64(len(body)) > u.policy.MaxResponseBodySize {
		uresp.Err = &UpstreamError{
			Kind: UpstreamBodyTooLarge,
		}

		return uresp
	}

	uresp.Body = body

	return uresp
}

func (u *httpUpstream) newRequest(ctx context.Context, original *http.Request, originalBody []byte) (*http.Request, error) {
	method := u.method
	if method == "" {
		// Fallback method.
		method = original.Method
	}

	// Send request body only for body-acceptable methods requests.
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		originalBody = nil
	}

	target, err := http.NewRequestWithContext(ctx, method, u.selectHost(), bytes.NewReader(originalBody))
	if err != nil {
		return nil, err
	}

	u.resolveQueryStrings(target, original)
	u.resolveHeaders(target, original)

	return target, nil
}

func (u *httpUpstream) selectHost() string {
	if len(u.hosts) == 1 {
		return u.hosts[0]
	}

	idx := atomic.AddUint64(&u.currentHostIdx, 1)
	host := u.hosts[idx%uint64(len(u.hosts))]

	u.log.Debug("new host selected", zap.String("host", host), zap.String("upstream", u.name))

	return host
}

func (u *httpUpstream) resolveQueryStrings(target, original *http.Request) {
	q := target.URL.Query()

	for _, fqs := range u.forwardQueryStrings {
		if fqs == "*" {
			q = original.URL.Query()
			break
		}

		if original.URL.Query().Get(fqs) == "" {
			continue
		}

		q.Add(fqs, original.URL.Query().Get(fqs))
	}

	target.URL.RawQuery = q.Encode()
}

func (u *httpUpstream) resolveHeaders(target, original *http.Request) {
	// Set forwarding headers
	for _, fw := range u.forwardHeaders {
		if fw == "*" {
			target.Header = original.Header.Clone()
			break
		}

		if strings.HasSuffix(fw, "*") {
			prefix := strings.TrimSuffix(fw, "*")

			for name, values := range original.Header {
				if strings.HasPrefix(name, prefix) {
					for _, v := range values {
						target.Header.Add(name, v)
					}
				}
			}

			continue
		}

		if original.Header.Get(fw) != "" {
			target.Header.Add(fw, original.Header.Get(fw))
		}
	}

	// Always forward these headers
	target.Header.Set("Content-Type", original.Header.Get("Content-Type"))
	target.Header.Set("Host", target.URL.Host)

	if ip, _, err := net.SplitHostPort(original.RemoteAddr); err == nil {
		target.Header.Add("X-Forwarded-For", ip)
	}
}

func (u *httpUpstream) isBreakerFailure(uerr *UpstreamError) bool {
	if uerr == nil || uerr.Err == nil {
		return false
	}

	if errors.Is(uerr.Err, context.Canceled) || errors.Is(uerr.Err, context.DeadlineExceeded) {
		return false
	}

	switch uerr.Kind {
	case UpstreamTimeout, UpstreamConnection, UpstreamBadStatus:
		return true
	default:
		return false
	}
}
