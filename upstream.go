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

type upstream interface {
	name() string
	call(ctx context.Context, original *http.Request, originalBody []byte) *upstreamResponse
}

type upstreamResponse struct {
	status  int
	headers http.Header
	body    []byte
	err     *upstreamError
}

type upstreamError struct {
	kind upstreamErrorKind
	err  error
}

func (ue *upstreamError) Error() string { return string(ue.kind) }
func (ue *upstreamError) Unwrap() error { return ue.err }

type upstreamErrorKind string

const (
	upstreamTimeout      upstreamErrorKind = "timeout"
	upstreamCanceled     upstreamErrorKind = "canceled"
	upstreamConnection   upstreamErrorKind = "connection"
	upstreamBadStatus    upstreamErrorKind = "bad_status"
	upstreamReadError    upstreamErrorKind = "read_error"
	upstreamBodyTooLarge upstreamErrorKind = "body_too_large"
	upstreamCircuitOpen  upstreamErrorKind = "circuit_open"
	upstreamInternal     upstreamErrorKind = "internal"
)

type httpUpstream struct {
	cfg            upstreamConfig
	state          upstreamState
	circuitBreaker *circuitbreaker.CircuitBreaker
	metrics        metric.Metrics
	log            *zap.Logger
	client         *http.Client
}

type upstreamConfig struct {
	id     string
	name   string
	hosts  []string
	path   string
	method string

	timeout        time.Duration
	forwardHeaders []string
	forwardQueries []string
	forwardParams  []string
	trustedProxies []*net.IPNet

	lbMode lbMode
	policy upstreamPolicy
}

type upstreamState struct {
	currentHostIdx    int64
	activeConnections []int64
}

func (u *httpUpstream) name() string { return u.cfg.name }

// call executes the request with retries and circuit breaker protection,
// then validates the final response against the upstream's own policy.
func (u *httpUpstream) call(ctx context.Context, original *http.Request, originalBody []byte) *upstreamResponse {
	log := u.log.With(
		zap.String("upstream", u.cfg.name),
		zap.String("request_id", requestIDFromContext(original.Context())),
	)

	resp := u.callWithRetry(ctx, original, originalBody, log)

	u.updateCircuitBreaker(resp, log)

	// Policy is applied after the circuit breaker update intentionally:
	// a misconfigured allowedStatuses or requireBody should not cause the breaker to open.
	u.applyPolicy(ctx, resp)

	return resp
}

func (u *httpUpstream) callWithRetry(ctx context.Context, original *http.Request, originalBody []byte, log *zap.Logger) *upstreamResponse {
	retry := u.cfg.policy.retry

	var resp *upstreamResponse

	for attempt := 0; attempt <= retry.maxRetries; attempt++ {
		if attempt > 0 {
			u.metrics.IncUpstreamRetriesTotal(routeFromContext(ctx), u.cfg.name)
		}

		if err := ctx.Err(); err != nil {
			return &upstreamResponse{err: &upstreamError{kind: upstreamCanceled, err: err}}
		}

		if u.circuitBreaker != nil && !u.circuitBreaker.Allow() {
			log.Error("circuit breaker open, request denied")
			u.metrics.SetCircuitBreakerState(u.cfg.name, float64(u.circuitBreaker.State()))

			return &upstreamResponse{
				err: &upstreamError{
					kind: upstreamCircuitOpen,
					err:  errors.New("upstream circuit breaker is open"),
				},
			}
		}

		resp = u.doCall(ctx, original, originalBody, log)

		if resp.err == nil && !slices.Contains(retry.retryOnStatuses, resp.status) {
			break
		}

		if attempt == retry.maxRetries {
			break
		}

		if retry.backoffDelay > 0 {
			select {
			case <-time.After(retry.backoffDelay):
			case <-ctx.Done():
				return &upstreamResponse{err: &upstreamError{kind: upstreamCanceled, err: ctx.Err()}}
			}
		}
	}

	return resp
}

func (u *httpUpstream) updateCircuitBreaker(resp *upstreamResponse, log *zap.Logger) {
	if u.circuitBreaker == nil {
		return
	}

	if resp.err != nil && u.isBreakerFailure(resp.err) {
		log.Error("upstream request failed, recording circuit breaker failure")
		u.circuitBreaker.OnFailure()
	} else {
		u.circuitBreaker.OnSuccess()
	}

	u.metrics.SetCircuitBreakerState(u.cfg.name, float64(u.circuitBreaker.State()))
}

// applyPolicy validates the response against the upstream's own policy rules.
// Violations are merged into resp.err so the original error kind is preserved.
func (u *httpUpstream) applyPolicy(ctx context.Context, resp *upstreamResponse) {
	var errs []error

	if u.cfg.policy.requireBody && len(resp.body) == 0 {
		errs = append(errs, errors.New("empty body not allowed by upstream policy"))
	}

	if len(u.cfg.policy.allowedStatuses) > 0 && !slices.Contains(u.cfg.policy.allowedStatuses, resp.status) {
		errs = append(errs, fmt.Errorf("status %d not in allowed list", resp.status))
	}

	if len(errs) == 0 {
		return
	}

	u.metrics.IncUpstreamErrorsTotal(routeFromContext(ctx), u.cfg.name, "policy_violation")

	combined := errors.Join(errs...)
	if resp.err == nil {
		resp.err = &upstreamError{err: combined}
	} else {
		resp.err.err = errors.Join(resp.err.err, combined)
	}
}

func (u *httpUpstream) doCall(ctx context.Context, original *http.Request, originalBody []byte, log *zap.Logger) *upstreamResponse {
	ctx, cancel := context.WithTimeout(ctx, u.cfg.timeout)
	defer cancel()

	selectedHost := u.selectHost(log)

	if u.cfg.lbMode == lbModeLeastConns {
		atomic.AddInt64(&u.state.activeConnections[selectedHost], 1)
		defer atomic.AddInt64(&u.state.activeConnections[selectedHost], -1)
	}

	req, err := u.newRequest(ctx, original, originalBody, u.cfg.hosts[selectedHost])
	if err != nil {
		return &upstreamResponse{err: &upstreamError{kind: upstreamInternal, err: err}}
	}

	httpResp, err := u.client.Do(req)
	if err != nil {
		log.Error("upstream request failed", zap.Error(err))
		return &upstreamResponse{err: &upstreamError{kind: u.classifyDoError(err), err: err}}
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= http.StatusInternalServerError {
		log.Error("upstream returned server error", zap.Int("status_code", httpResp.StatusCode))

		return &upstreamResponse{
			status: httpResp.StatusCode,
			err:    &upstreamError{kind: upstreamBadStatus, err: fmt.Errorf("upstream returned %d", httpResp.StatusCode)},
		}
	}

	body, uerr := u.readBody(ctx, httpResp.Body, log)
	if uerr != nil {
		return &upstreamResponse{status: httpResp.StatusCode, err: uerr}
	}

	return &upstreamResponse{
		status:  httpResp.StatusCode,
		headers: u.filterHeaders(httpResp.Header),
		body:    body,
	}
}

func (u *httpUpstream) readBody(ctx context.Context, body io.ReadCloser, log *zap.Logger) ([]byte, *upstreamError) {
	reader := io.Reader(body)

	if u.cfg.policy.maxResponseBodySize > 0 {
		log.Debug("applying response body size limit", zap.Int64("limit", u.cfg.policy.maxResponseBodySize))
		reader = io.LimitReader(reader, u.cfg.policy.maxResponseBodySize+1)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &upstreamError{kind: upstreamCanceled, err: ctx.Err()}
		}

		return nil, &upstreamError{kind: upstreamReadError, err: err}
	}

	if u.cfg.policy.maxResponseBodySize > 0 && int64(len(data)) > u.cfg.policy.maxResponseBodySize {
		return nil, &upstreamError{
			kind: upstreamBodyTooLarge,
			err:  fmt.Errorf("response size exceeds limit of %d bytes", u.cfg.policy.maxResponseBodySize),
		}
	}

	return data, nil
}

func (u *httpUpstream) filterHeaders(headers http.Header) http.Header {
	if len(u.cfg.policy.headerBlacklist) == 0 {
		return headers.Clone()
	}

	filtered := make(http.Header, len(headers))
	for header, values := range headers {
		if _, blocked := u.cfg.policy.headerBlacklist[header]; !blocked {
			filtered[header] = values
		}
	}

	return filtered
}

func (u *httpUpstream) classifyDoError(err error) upstreamErrorKind {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return upstreamTimeout
	case errors.Is(err, context.Canceled):
		return upstreamCanceled
	default:
		return upstreamConnection
	}
}

func (u *httpUpstream) isBreakerFailure(uerr *upstreamError) bool {
	if uerr == nil {
		return false
	}

	switch uerr.kind {
	case upstreamTimeout, upstreamConnection, upstreamBadStatus:
		return true
	case upstreamCanceled, upstreamReadError, upstreamBodyTooLarge, upstreamCircuitOpen, upstreamInternal:
		return false
	default:
		return false
	}
}

func (u *httpUpstream) newRequest(ctx context.Context, original *http.Request, originalBody []byte, targetHost string) (*http.Request, error) {
	path := expandPathParams(u.cfg.path, original)
	path = strings.TrimPrefix(path, "/")

	var hostPath string
	if strings.HasSuffix(targetHost, "/") {
		hostPath = targetHost + path
	} else {
		hostPath = targetHost + "/" + path
	}

	method := u.cfg.method
	if method == "" {
		method = original.Method
	}

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

		if value := chi.URLParam(req, name); value != "" {
			return value
		}

		return match
	})
}

func (u *httpUpstream) selectHost(log *zap.Logger) int64 {
	if len(u.cfg.hosts) == 1 {
		return 0
	}

	var selected int64

	switch u.cfg.lbMode {
	case lbModeRoundRobin:
		idx := atomic.AddInt64(&u.state.currentHostIdx, 1)
		selected = idx % int64(len(u.cfg.hosts))
	case lbModeLeastConns:
		var minConns int64 = math.MaxInt64

		for i := range u.cfg.hosts {
			if c := atomic.LoadInt64(&u.state.activeConnections[i]); c < minConns {
				minConns = c
				selected = int64(i)
			}
		}
	}

	log.Debug("host selected",
		zap.String("host", u.cfg.hosts[selected]),
		zap.String("upstream", u.cfg.name),
	)

	return selected
}

func (u *httpUpstream) resolveQueries(target, original *http.Request) {
	targetQ := target.URL.Query()
	originalQ := original.URL.Query()

	for _, pattern := range u.cfg.forwardQueries {
		if pattern == "*" {
			target.URL.RawQuery = originalQ.Encode()
			return
		}

		if v := originalQ.Get(pattern); v != "" {
			targetQ.Add(pattern, v)
		}
	}

	for _, param := range u.cfg.forwardParams {
		if param == "*" {
			if rctx := chi.RouteContext(original.Context()); rctx != nil {
				for i, key := range rctx.URLParams.Keys {
					targetQ.Set(key, rctx.URLParams.Values[i])
				}
			}

			target.URL.RawQuery = targetQ.Encode()

			return
		}

		if v := chi.URLParam(original, param); v != "" {
			targetQ.Set(param, v)
		}
	}

	target.URL.RawQuery = targetQ.Encode()
}

func (u *httpUpstream) resolveHeaders(target, original *http.Request) error {
	for _, pattern := range u.cfg.forwardHeaders {
		if pattern == "*" {
			target.Header = original.Header.Clone()
			break
		}

		if strings.HasSuffix(pattern, "*") {
			u.forwardHeadersByPrefix(target, original.Header, strings.TrimSuffix(pattern, "*"))
			continue
		}

		if v := original.Header.Get(pattern); v != "" {
			target.Header.Add(pattern, v)
		}
	}

	target.Header.Set("Content-Type", original.Header.Get("Content-Type"))

	clientIP := clientIPFromContext(original.Context())
	if clientIP == "" {
		clientIP = extractClientIP(original)
	}

	parsedClientIP := net.ParseIP(clientIP)
	if parsedClientIP == nil {
		return fmt.Errorf("cannot parse client IP %q", clientIP)
	}

	isTLS := original.TLS != nil
	port := u.resolvePort(u.effectiveHost(original), isTLS)
	proto := "http"

	if isTLS {
		proto = "https"
	}

	if !u.isTrustedProxy(parsedClientIP) {
		u.setUntrustedForwardingHeaders(original, target, clientIP, proto, port)
	} else {
		u.appendTrustedForwardingHeaders(original, target, clientIP, proto, port)
	}

	return nil
}

func (u *httpUpstream) forwardHeadersByPrefix(target *http.Request, src http.Header, prefix string) {
	for name, values := range src {
		if strings.HasPrefix(name, prefix) {
			for _, v := range values {
				target.Header.Add(name, v)
			}
		}
	}
}

func (u *httpUpstream) setUntrustedForwardingHeaders(original, target *http.Request, clientIP, proto, port string) {
	host := u.effectiveHost(original)

	target.Header.Set("X-Forwarded-For", clientIP)
	target.Header.Set("X-Forwarded-Proto", proto)
	target.Header.Set("X-Forwarded-Host", host)
	target.Header.Set("X-Forwarded-Port", port)
	target.Header.Set("Forwarded", fmt.Sprintf("for=%s; proto=%s; host=%s", clientIP, proto, host))
}

func (u *httpUpstream) appendTrustedForwardingHeaders(original, target *http.Request, clientIP, proto, port string) {
	if xff := original.Header.Get("X-Forwarded-For"); xff != "" {
		target.Header.Set("X-Forwarded-For", xff+", "+clientIP)
	} else {
		target.Header.Set("X-Forwarded-For", clientIP)
	}

	if p := original.Header.Get("X-Forwarded-Proto"); p == "http" || p == "https" {
		proto = p
	}
	target.Header.Set("X-Forwarded-Proto", proto)

	host := u.effectiveHost(original)
	if h := original.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	target.Header.Set("X-Forwarded-Host", host)

	if p := original.Header.Get("X-Forwarded-Port"); isValidPort(p) {
		target.Header.Set("X-Forwarded-Port", p)
	} else {
		target.Header.Set("X-Forwarded-Port", port)
	}

	hop := fmt.Sprintf("for=%s; proto=%s; host=%s", clientIP, proto, host)
	if fwd := original.Header.Get("Forwarded"); fwd != "" {
		target.Header.Set("Forwarded", fwd+", "+hop)
	} else {
		target.Header.Set("Forwarded", hop)
	}
}

func (u *httpUpstream) isTrustedProxy(ip net.IP) bool {
	for _, cidr := range u.cfg.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}

func (u *httpUpstream) resolvePort(host string, isTLS bool) string {
	_, port, err := net.SplitHostPort(host)
	if err != nil {
		if isTLS {
			return "443"
		}

		return "80"
	}

	return port
}

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

// proxy implements proxyCapable for streaming passthrough flows.
func (u *httpUpstream) proxy(ctx context.Context, w http.ResponseWriter, original *http.Request) error {
	selectedHost := u.selectHost(u.log)
	host := u.cfg.hosts[selectedHost]

	path := expandPathParams(u.cfg.path, original)
	path = strings.TrimPrefix(path, "/")

	hostPath := host
	if !strings.HasSuffix(host, "/") {
		hostPath += "/"
	}

	hostPath += path

	method := u.cfg.method
	if method == "" {
		method = original.Method
	}

	req, err := http.NewRequestWithContext(ctx, method, hostPath, original.Body)
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}

	req.ContentLength = original.ContentLength
	req.TransferEncoding = original.TransferEncoding

	u.resolveQueries(req, original)

	if err = u.resolveHeaders(req, original); err != nil {
		return fmt.Errorf("resolve headers: %w", err)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("upstream call: %w", err)
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		if _, skip := hopByHopHeaders[k]; skip {
			continue
		}

		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)

	if err = streamCopy(w, resp.Body); err != nil {
		return fmt.Errorf("stream body: %w", err)
	}

	return nil
}
