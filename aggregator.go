package kono

import (
	"encoding/json"
	"net/http"
	"slices"

	"go.uber.org/zap"
)

type aggregatedResponse struct {
	data    json.RawMessage
	headers http.Header
	errors  []ClientError
	partial bool
}

type aggregator interface {
	aggregate(upstreams []upstream, responses []upstreamResponse, agg aggregation, log *zap.Logger) aggregatedResponse
}

type defaultAggregator struct{}

var (
	respInternalError = aggregatedResponse{
		errors: []ClientError{ClientErrInternal},
	}

	respUpstreamMalformedError = aggregatedResponse{
		errors: []ClientError{ClientErrUpstreamMalformed},
	}
)

// aggregate combines multiple upstream responses based on the flow's strategy.
// A single response is returned as-is. Multiple responses are aggregated either
// by merging JSON objects ("merge"), creating a JSON array ("array"),
// or namespacing each upstream under its name ("namespace").
// Upstream errors respect bestEffort: partial results may be returned
// if allowed, otherwise a single error response is returned.
func (a *defaultAggregator) aggregate(upstreams []upstream, responses []upstreamResponse, agg aggregation, log *zap.Logger) aggregatedResponse {
	if len(responses) == 1 {
		return a.rawResponse(responses[0])
	}

	switch agg.strategy {
	case strategyMerge:
		return a.merged(responses, agg, log)
	case strategyArray:
		return a.arrayed(responses, agg, log)
	case strategyNamespace:
		return a.namespaced(upstreams, responses, agg, log)
	default:
		log.Error("unknown aggregation strategy", zap.String("strategy", agg.strategy.String()))
		return aggregatedResponse{}
	}
}

func (a *defaultAggregator) rawResponse(resp upstreamResponse) aggregatedResponse {
	if resp.err != nil {
		return aggregatedResponse{errors: []ClientError{a.mapUpstreamError(resp.err)}}
	}

	return aggregatedResponse{
		data:    resp.body,
		headers: mergeSuccessfulHeaders([]upstreamResponse{resp}),
	}
}

// field holds a merged JSON value and the index of the upstream that produced it.
type field struct {
	value interface{}
	owner int // upstream index, used by conflictPolicyPrefer.
}

func (a *defaultAggregator) merged(responses []upstreamResponse, agg aggregation, log *zap.Logger) aggregatedResponse {
	fields, aggErrors, hasSuccessful, early := a.collectFields(responses, agg, log)
	if early != nil {
		return *early
	}

	flat := make(map[string]interface{}, len(fields))
	for k, f := range fields {
		flat[k] = f.value
	}

	data, err := json.Marshal(flat)
	if err != nil {
		return respInternalError
	}

	return aggregatedResponse{
		data:    data,
		headers: mergeSuccessfulHeaders(responses),
		errors:  dedupeErrors(aggErrors),
		partial: len(aggErrors) > 0 && hasSuccessful,
	}
}

// collectFields iterates responses and builds the merged field map applying the conflict policy.
// Returns (fields, aggErrors, hasSuccessful, early) where early is non-nil when the caller
// must return immediately — bestEffort=false failure or a conflictPolicyError conflict.
func (a *defaultAggregator) collectFields(
	responses []upstreamResponse,
	agg aggregation,
	log *zap.Logger,
) (fields map[string]field, aggErrors []ClientError, hasSuccessful bool, early *aggregatedResponse) {
	fields = make(map[string]field)

	for idx, resp := range responses {
		if resp.err != nil {
			log.Warn("upstream has errors",
				zap.Bool("best_effort", agg.bestEffort),
				zap.String("upstream_error", resp.err.Unwrap().Error()),
				zap.String("client_error", a.mapUpstreamError(resp.err).String()),
			)

			if !agg.bestEffort {
				r := aggregatedResponse{errors: dedupeErrors(a.collectErrors(responses))}
				return nil, nil, false, &r
			}

			aggErrors = append(aggErrors, a.mapUpstreamError(resp.err))

			continue
		}

		hasSuccessful = true

		if resp.body == nil {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal(resp.body, &obj); err != nil {
			log.Error("cannot unmarshal upstream response", zap.Error(err))

			if !agg.bestEffort {
				return nil, nil, false, &respUpstreamMalformedError
			}

			aggErrors = append(aggErrors, ClientErrUpstreamMalformed)

			continue
		}

		for k, v := range obj {
			if r := a.applyConflictPolicy(fields, k, field{value: v, owner: idx}, agg); r != nil {
				return nil, nil, false, r
			}
		}
	}

	return fields, aggErrors, hasSuccessful, nil
}

// applyConflictPolicy writes incoming into fields[k] according to the configured policy.
// Returns non-nil only for conflictPolicyError when a conflict is detected.
func (a *defaultAggregator) applyConflictPolicy(fields map[string]field, k string, incoming field, agg aggregation) *aggregatedResponse {
	current, exists := fields[k]
	if !exists {
		fields[k] = incoming
		return nil
	}

	switch agg.conflictPolicy {
	case conflictPolicyOverwrite:
		fields[k] = incoming
	case conflictPolicyFirst:
		// keep existing value — do nothing.
	case conflictPolicyError:
		r := aggregatedResponse{errors: []ClientError{ClientErrValueConflict}}
		return &r
	case conflictPolicyPrefer:
		// Replace only when the incoming upstream is preferred and the current one is not.
		if current.owner != agg.preferredUpstream && incoming.owner == agg.preferredUpstream {
			fields[k] = incoming
		}
	}

	return nil
}

func (a *defaultAggregator) arrayed(responses []upstreamResponse, agg aggregation, log *zap.Logger) aggregatedResponse {
	var (
		arr           []json.RawMessage
		aggErrors     []ClientError
		hasSuccessful bool
	)

	for _, resp := range responses {
		if resp.err != nil {
			log.Warn("upstream has errors",
				zap.Bool("best_effort", agg.bestEffort),
				zap.String("upstream_error", resp.err.Unwrap().Error()),
				zap.String("client_error", a.mapUpstreamError(resp.err).String()),
			)

			if !agg.bestEffort {
				return aggregatedResponse{errors: dedupeErrors(a.collectErrors(responses))}
			}

			aggErrors = append(aggErrors, a.mapUpstreamError(resp.err))

			continue
		}

		hasSuccessful = true

		if resp.body != nil {
			arr = append(arr, resp.body)
		}
	}

	data, err := json.Marshal(arr)
	if err != nil {
		return respUpstreamMalformedError
	}

	return aggregatedResponse{
		data:    data,
		headers: mergeSuccessfulHeaders(responses),
		errors:  dedupeErrors(aggErrors),
		partial: len(aggErrors) > 0 && hasSuccessful,
	}
}

func (a *defaultAggregator) namespaced(upstreams []upstream, responses []upstreamResponse, agg aggregation, _ *zap.Logger) aggregatedResponse {
	result := make(map[string]json.RawMessage, len(responses))

	var (
		aggErrors     []ClientError
		hasSuccessful bool
	)

	for i, resp := range responses {
		name := upstreams[i].name()

		if resp.err != nil {
			if !agg.bestEffort {
				return aggregatedResponse{errors: []ClientError{a.mapUpstreamError(resp.err)}}
			}

			aggErrors = append(aggErrors, a.mapUpstreamError(resp.err))

			continue
		}

		hasSuccessful = true

		if resp.body == nil {
			result[name] = json.RawMessage("null")
		} else {
			result[name] = resp.body
		}
	}

	data, err := json.Marshal(result)
	if err != nil {
		return respInternalError
	}

	return aggregatedResponse{
		data:    data,
		headers: mergeSuccessfulHeaders(responses),
		errors:  dedupeErrors(aggErrors),
		partial: len(aggErrors) > 0 && hasSuccessful,
	}
}

// collectErrors collects mapped ClientErrors from all failed responses.
// Used when bestEffort=false to return all upstream errors at once.
func (a *defaultAggregator) collectErrors(responses []upstreamResponse) []ClientError {
	errs := make([]ClientError, 0, len(responses))

	for _, r := range responses {
		if r.err != nil {
			errs = append(errs, a.mapUpstreamError(r.err))
		}
	}

	return errs
}

func mergeSuccessfulHeaders(responses []upstreamResponse) http.Header {
	merged := make(http.Header)

	for _, resp := range responses {
		if resp.err != nil {
			continue
		}

		for k, v := range resp.headers {
			merged[k] = v
		}
	}

	return merged
}

func (a *defaultAggregator) mapUpstreamError(err *upstreamError) ClientError {
	if err == nil {
		return ClientErrInternal
	}

	switch err.kind {
	case upstreamTimeout, upstreamConnection, upstreamCircuitOpen:
		return ClientErrUpstreamUnavailable
	case upstreamBadStatus:
		return ClientErrUpstreamError
	case upstreamBodyTooLarge:
		return ClientErrUpstreamBodyTooLarge
	case upstreamCanceled:
		return ClientErrAborted
	case upstreamReadError, upstreamInternal:
		return ClientErrInternal
	default:
		return ClientErrInternal
	}
}

func dedupeErrors(errs []ClientError) []ClientError {
	out := make([]ClientError, 0, len(errs))

	for _, e := range errs {
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}

	return out
}
