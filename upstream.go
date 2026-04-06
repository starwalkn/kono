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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"
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
	cfg   upstreamConfig
	state upstreamState

	circuitBreaker *circuitbreaker.CircuitBreaker

	metrics metric.Metrics
	log     *zap.Logger
	client  *http.Client
}

type upstreamConfig struct {
	id     string // UUID for internal usage.
	name   string // For logs.
	hosts  []string
	path   string
	method string

	timeout        time.Duration
	forwardHeaders []string
	forwardQueries []string
	forwardParams  []string
	trustedProxies []*net.IPNet

	lbMode LBMode
	policy Policy
}

type upstreamState struct {
	currentHostIdx    int64   // Round Robin.
	activeConnections []int64 // Least Connections.
}

func (u *httpUpstream) Name() string   { return u.cfg.name }
func (u *httpUpstream) Policy() Policy { return u.cfg.policy }

func (u *httpUpstream) Call(ctx context.Context, original *http.Request, originalBody []byte) *UpstreamResponse {
	log := u.log.With(
		zap.String("upstream", u.cfg.name),
		zap.String("request_id", requestIDFromContext(original.Context())),
	)

	var resp *UpstreamResponse

	retryPolicy := u.cfg.policy.Retry

	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		if attempt > 0 {
			u.metrics.IncUpstreamRetriesTotal(routeFromContext(ctx), u.cfg.name)
		}

		if err := ctx.Err(); err != nil {
			return &UpstreamResponse{
				Err: &UpstreamError{
					Kind: UpstreamCanceled,
					Err:  err,
				},
			}
		}

		if u.circuitBreaker != nil && !u.circuitBreaker.Allow() {
			log.Error("circuit breaker deny request")
			u.metrics.SetCircuitBreakerState(u.cfg.name, float64(u.circuitBreaker.State()))

			return &UpstreamResponse{
				Err: &UpstreamError{
					Kind: UpstreamCircuitOpen,
					Err:  errors.New("upstream circuit breaker is open"),
				},
			}
		}

		resp = u.call(ctx, original, originalBody, log)

		if resp.Err == nil && !slices.Contains(retryPolicy.RetryOnStatuses, resp.Status) {
			break
		}

		if attempt == retryPolicy.MaxRetries {
			break
		}

		if retryPolicy.BackoffDelay > 0 {
			select {
			case <-time.After(retryPolicy.BackoffDelay):
			case <-ctx.Done():
				return &UpstreamResponse{
					Err: &UpstreamError{Kind: UpstreamCanceled, Err: ctx.Err()},
				}
			}
		}
	}

	if u.circuitBreaker != nil {
		if resp.Err != nil && u.isBreakerFailure(resp.Err) {
			log.Error("upstream request failed, opening circuit breaker")
			u.circuitBreaker.OnFailure()
		} else {
			u.circuitBreaker.OnSuccess()
		}

		u.metrics.SetCircuitBreakerState(u.cfg.name, float64(u.circuitBreaker.State()))
	}

	return resp
}

func (u *httpUpstream) call(ctx context.Context, original *http.Request, originalBody []byte, log *zap.Logger) *UpstreamResponse {
	ctx, cancel := context.WithTimeout(ctx, u.cfg.timeout)
	defer cancel()

	selectedHost := u.selectHost(log)

	if u.cfg.lbMode == lbModeLeastConns {
		atomic.AddInt64(&u.state.activeConnections[selectedHost], 1)
		defer atomic.AddInt64(&u.state.activeConnections[selectedHost], -1)
	}

	req, upstreamErr := u.newRequest(ctx, original, originalBody, u.cfg.hosts[selectedHost])
	if upstreamErr != nil {
		return &UpstreamResponse{
			Err: &UpstreamError{
				Kind: UpstreamInternal,
				Err:  upstreamErr,
			},
		}
	}

	httpResp, upstreamErr := u.client.Do(req)
	if upstreamErr != nil {
		log.Error("upstream request failed", zap.Error(upstreamErr))

		return &UpstreamResponse{
			Err: &UpstreamError{
				Kind: u.classifyDoError(upstreamErr),
				Err:  upstreamErr,
			},
		}
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= http.StatusInternalServerError {
		log.Error("upstream returned server error", zap.Int("status_code", httpResp.StatusCode))

		return &UpstreamResponse{
			Status: httpResp.StatusCode,
			Err: &UpstreamError{
				Kind: UpstreamBadStatus,
				Err:  fmt.Errorf("upstream returned %d", httpResp.StatusCode),
			},
		}
	}

	body, err := u.readBody(ctx, httpResp.Body, log)
	if err != nil {
		return &UpstreamResponse{
			Status: httpResp.StatusCode,
			Err:    err,
		}
	}

	upstreamResp := &UpstreamResponse{
		Status:  httpResp.StatusCode,
		Headers: u.filterHeaders(httpResp.Header),
		Body:    body,
	}

	return upstreamResp
}

func (u *httpUpstream) readBody(ctx context.Context, body io.ReadCloser, log *zap.Logger) ([]byte, *UpstreamError) {
	reader := io.Reader(body)

	if u.cfg.policy.MaxResponseBodySize > 0 {
		log.Debug("applying response body size limit", zap.Int64("limit", u.cfg.policy.MaxResponseBodySize))
		reader = io.LimitReader(reader, u.cfg.policy.MaxResponseBodySize+1)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &UpstreamError{
				Kind: UpstreamCanceled,
				Err:  ctx.Err(),
			}
		}

		return nil, &UpstreamError{
			Kind: UpstreamReadError,
			Err:  err,
		}
	}

	if u.cfg.policy.MaxResponseBodySize > 0 && int64(len(data)) > u.cfg.policy.MaxResponseBodySize {
		return nil, &UpstreamError{
			Kind: UpstreamBodyTooLarge,
			Err:  fmt.Errorf("data size exceeds the set limit %d", u.cfg.policy.MaxResponseBodySize),
		}
	}

	return data, nil
}

func (u *httpUpstream) filterHeaders(headers http.Header) http.Header {
	blacklist := u.cfg.policy.HeaderBlacklist

	if len(blacklist) == 0 {
		return headers.Clone()
	}

	filtered := make(http.Header, len(headers))
	for header, values := range headers {
		if _, blocked := blacklist[header]; !blocked {
			filtered[header] = values
		}
	}

	return filtered
}

func (u *httpUpstream) classifyDoError(err error) UpstreamErrorKind {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return UpstreamTimeout
	case errors.Is(err, context.Canceled):
		return UpstreamCanceled
	default:
		return UpstreamConnection
	}
}

func (u *httpUpstream) newRequest(ctx context.Context, original *http.Request, originalBody []byte, targetHost string) (*http.Request, error) {
	var hostPath string

	path := expandPathParams(u.cfg.path, original)
	path = strings.TrimPrefix(path, "/")

	if strings.HasSuffix(targetHost, "/") {
		hostPath = targetHost + path
	} else {
		hostPath = targetHost + "/" + path
	}

	method := u.cfg.method
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

var pathParamRegexp = regexp.MustCompile(`\{([^}]+)\}`)

func expandPathParams(path string, req *http.Request) string {
	return pathParamRegexp.ReplaceAllStringFunc(path, func(match string) string {
		name := match[1 : len(match)-1]

		value := chi.URLParam(req, name)
		if value == "" {
			return match
		}

		return value
	})
}

// selectHost returns the index of selected host in hosts slice.
func (u *httpUpstream) selectHost(log *zap.Logger) int64 {
	if len(u.cfg.hosts) == 1 {
		return 0
	}

	var selectedHost int64

	switch u.cfg.lbMode {
	case lbModeRoundRobin:
		idx := atomic.AddInt64(&u.state.currentHostIdx, 1)
		selectedHost = idx % int64(len(u.cfg.hosts))
	case lbModeLeastConns:
		var (
			best           int64
			minActiveConns int64 = math.MaxInt64
		)

		for i := range u.cfg.hosts {
			curHostActiveConns := atomic.LoadInt64(&u.state.activeConnections[i])

			if curHostActiveConns < minActiveConns {
				minActiveConns = curHostActiveConns
				best = int64(i)
			}
		}

		selectedHost = best
	default:
		selectedHost = 0
	}

	log.Debug("new host selected", zap.String("host", u.cfg.hosts[selectedHost]), zap.String("upstream", u.cfg.name))

	return selectedHost
}

func (u *httpUpstream) resolveQueries(target, original *http.Request) {
	var (
		targetQuery   = target.URL.Query()
		originalQuery = original.URL.Query()
	)

	for _, fqs := range u.cfg.forwardQueries {
		if fqs == "*" {
			targetQuery = originalQuery
			break
		}

		if originalQuery.Get(fqs) == "" {
			continue
		}

		targetQuery.Add(fqs, originalQuery.Get(fqs))
	}

	for _, param := range u.cfg.forwardParams {
		if param == "*" {
			if rctx := chi.RouteContext(original.Context()); rctx != nil {
				for i, key := range rctx.URLParams.Keys {
					targetQuery.Set(key, rctx.URLParams.Values[i])
				}
			}

			break
		}

		if v := chi.URLParam(original, param); v != "" {
			targetQuery.Set(param, v)
		}
	}

	target.URL.RawQuery = targetQuery.Encode()
}

func (u *httpUpstream) resolveHeaders(target, original *http.Request) error {
	// Set forwarding headers
	for _, fw := range u.cfg.forwardHeaders {
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

	clientIP := clientIPFromContext(original.Context())
	if clientIP == "" {
		clientIP = extractClientIP(original)
	}

	remoteIP := net.ParseIP(clientIP)
	if remoteIP == nil {
		return fmt.Errorf("cannot parse client ip '%s", clientIP)
	}

	port := u.resolvePort(original)

	proto := "http"
	if original.TLS != nil {
		proto = "https"
	}

	// For untrusted proxies that are not included in the list of configured TrustedProxies,
	// we cannot blindly trust their X-Forwarded-* headers.
	// Therefore, in cases where remote_addr is not included in the TrustedProxies list,
	// we determine the necessary header values ourselves, ignoring similar incoming headers
	if !u.isTrustedProxy(remoteIP) {
		target.Header.Set("X-Forwarded-For", clientIP)
		target.Header.Set("X-Forwarded-Proto", proto)
		target.Header.Set("X-Forwarded-Host", u.effectiveHost(original))
		target.Header.Set("X-Forwarded-Port", port)

		forwarded := fmt.Sprintf("for=%s; proto=%s; host=%s", clientIP, proto, u.effectiveHost(original))
		target.Header.Set("Forwarded", forwarded)
	} else {
		if incomingXFF := original.Header.Get("X-Forwarded-For"); incomingXFF != "" {
			target.Header.Set("X-Forwarded-For", incomingXFF+", "+clientIP)
		} else {
			target.Header.Set("X-Forwarded-For", clientIP)
		}

		if incomingProto := original.Header.Get("X-Forwarded-Proto"); incomingProto == "http" || incomingProto == "https" {
			proto = incomingProto
		}
		target.Header.Set("X-Forwarded-Proto", proto)

		host := u.effectiveHost(original)
		if incomingHost := original.Header.Get("X-Forwarded-Host"); incomingHost != "" {
			host = incomingHost
		}
		target.Header.Set("X-Forwarded-Host", host)

		if incomingPort := original.Header.Get("X-Forwarded-Port"); isValidPort(incomingPort) {
			target.Header.Set("X-Forwarded-Port", incomingPort)
		} else {
			target.Header.Set("X-Forwarded-Port", port)
		}

		newHop := fmt.Sprintf("for=%s; proto=%s; host=%s", clientIP, proto, host)
		if incomingForwarded := original.Header.Get("Forwarded"); incomingForwarded != "" {
			target.Header.Set("Forwarded", incomingForwarded+", "+newHop)
		} else {
			target.Header.Set("Forwarded", newHop)
		}
	}

	return nil
}

func (u *httpUpstream) isTrustedProxy(ip net.IP) bool {
	for _, cidr := range u.cfg.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}

func (u *httpUpstream) resolvePort(req *http.Request) string {
	_, port, err := net.SplitHostPort(u.effectiveHost(req))
	if err != nil {
		if req.TLS != nil {
			port = "443"
		} else {
			port = "80"
		}
	}

	return port
}

// effectiveHost returns the host from req.Host if set,
// falling back to req.URL.Host for client-created requests.
func (u *httpUpstream) effectiveHost(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}

	return req.URL.Host
}

func isValidPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535
}

func (u *httpUpstream) isBreakerFailure(uerr *UpstreamError) bool {
	if uerr == nil {
		return false
	}

	switch uerr.Kind {
	case UpstreamTimeout, UpstreamConnection, UpstreamBadStatus:
		return true
	case UpstreamCanceled, UpstreamBodyTooLarge, UpstreamReadError, UpstreamInternal:
		return false
	default:
		return false
	}
}
