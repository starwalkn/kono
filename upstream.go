package kono

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
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
	id             string // UUID for internal usage.
	name           string // For logs.
	hosts          []string
	path           string
	method         string
	timeout        time.Duration
	forwardHeaders []string
	forwardQueries []string
	trustedProxies []*net.IPNet
	policy         Policy

	currentHostIdx    int64   // Round Robin.
	activeConnections []int64 // Least Connections.

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

	selectedHost := u.selectHost()

	if u.policy.LoadBalancing.Mode == LBModeLeastConns {
		atomic.AddInt64(&u.activeConnections[selectedHost], 1)
		defer atomic.AddInt64(&u.activeConnections[selectedHost], -1)
	}

	req, err := u.newRequest(ctx, original, originalBody, u.hosts[selectedHost])
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

func (u *httpUpstream) newRequest(ctx context.Context, original *http.Request, originalBody []byte, targetHost string) (*http.Request, error) {
	var hostPath string

	path := strings.TrimPrefix(u.path, "/")
	if strings.HasSuffix(targetHost, "/") {
		hostPath = targetHost + path
	} else {
		hostPath = targetHost + "/" + path
	}

	method := u.method
	if method == "" {
		// Fallback method
		method = original.Method
	}

	// Send request body only for body-acceptable methods requests
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		originalBody = nil
	}

	target, err := http.NewRequestWithContext(ctx, method, hostPath, bytes.NewReader(originalBody))
	if err != nil {
		return nil, err
	}

	u.resolveQueries(target, original)
	if err = u.resolveHeaders(target, original); err != nil {
		return nil, fmt.Errorf("cannot resolve headers: %w", err)
	}

	return target, nil
}

// selectHost returns the index of selected host in hosts slice.
func (u *httpUpstream) selectHost() int64 {
	if len(u.hosts) == 1 {
		return 0
	}

	var selectedHost int64

	switch u.policy.LoadBalancing.Mode {
	case LBModeRoundRobin:
		idx := atomic.AddInt64(&u.currentHostIdx, 1)
		selectedHost = idx % int64(len(u.hosts))
	case LBModeLeastConns:
		var (
			best           int64
			minActiveConns int64 = math.MaxInt64
		)

		for i := range u.hosts {
			curHostActiveConns := atomic.LoadInt64(&u.activeConnections[i])

			if curHostActiveConns < minActiveConns {
				minActiveConns = curHostActiveConns
				best = int64(i)
			}
		}

		selectedHost = best
	default:
		selectedHost = 0
	}

	u.log.Debug("new host selected", zap.String("host", u.hosts[selectedHost]), zap.String("upstream", u.name))

	return selectedHost
}

func (u *httpUpstream) resolveQueries(target, original *http.Request) {
	q := target.URL.Query()

	for _, fqs := range u.forwardQueries {
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

func (u *httpUpstream) resolveHeaders(target, original *http.Request) error {
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

	target.Header.Set("Content-Type", original.Header.Get("Content-Type"))

	remoteHost, _, err := net.SplitHostPort(original.RemoteAddr)
	if err != nil {
		return fmt.Errorf("cannot split remote_addr '%s' to host port: %w", original.RemoteAddr, err)
	}

	remoteIP := net.ParseIP(remoteHost)
	if remoteIP == nil {
		return fmt.Errorf("cannot parse remote_addr '%s' ip", remoteHost)
	}

	port := u.resolvePort(original)

	proto := "http"
	if original.TLS != nil {
		proto = "https"
	}

	clientIP := remoteIP.String()

	// For untrusted proxies that are not included in the list of configured TrustedProxies,
	// we cannot blindly trust their X-Forwarded-* headers.
	// Therefore, in cases where remote_addr is not included in the TrustedProxies list,
	// we determine the necessary header values ourselves, ignoring similar incoming headers.
	if !u.isTrustedProxy(remoteIP) {
		target.Header.Set("X-Forwarded-For", clientIP)
		target.Header.Set("X-Forwarded-Proto", proto)
		target.Header.Set("X-Forwarded-Host", original.Host)
		target.Header.Set("X-Forwarded-Port", port)
	} else {
		if incomingXFF := original.Header.Get("X-Forwarded-For"); incomingXFF != "" {
			target.Header.Set("X-Forwarded-For", incomingXFF+", "+clientIP)
		} else {
			target.Header.Set("X-Forwarded-For", clientIP)
		}

		if incomingProto := original.Header.Get("X-Forwarded-Proto"); incomingProto == "http" || incomingProto == "https" {
			target.Header.Set("X-Forwarded-Proto", incomingProto)
		} else {
			target.Header.Set("X-Forwarded-Proto", proto)
		}

		if incomingHost := original.Header.Get("X-Forwarded-Host"); incomingHost != "" {
			target.Header.Set("X-Forwarded-Host", incomingHost)
		} else {
			target.Header.Set("X-Forwarded-Host", original.Host)
		}

		if incomingPort := original.Header.Get("X-Forwarded-Port"); incomingPort != "" {
			target.Header.Set("X-Forwarded-Port", incomingPort)
		} else {
			target.Header.Set("X-Forwarded-Port", port)
		}
	}

	return nil
}

func (u *httpUpstream) isTrustedProxy(ip net.IP) bool {
	for _, cidr := range u.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}

func (u *httpUpstream) resolvePort(req *http.Request) string {
	_, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		if req.TLS != nil {
			port = "443"
		} else {
			port = "80"
		}
	}

	return port
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
