package kono

import (
	"encoding/json"
	"net/http"
	"slices"

	"go.uber.org/zap"
)

type AggregatedResponse struct {
	Data    json.RawMessage
	Headers http.Header
	Errors  []ClientError
	Partial bool
}

type aggregator interface {
	aggregate(upstreams []Upstream, responses []UpstreamResponse, aggregation Aggregation, log *zap.Logger) AggregatedResponse
}

type defaultAggregator struct{}

var (
	respInternalError = AggregatedResponse{
		Errors: []ClientError{ClientErrInternal},
	}

	respUpstreamMalformedError = AggregatedResponse{
		Errors: []ClientError{ClientErrUpstreamMalformed},
	}
)

// aggregate combines multiple upstream responses based on the route's strategy.
// Single responses are returned as-is. Multiple responses are aggregated either
// by merging JSON objects ("merge") or creating a JSON array ("array").
// Upstream errors respect bestEffort: partial results may be included
// if allowed; otherwise a single error response is returned.
func (a *defaultAggregator) aggregate(upstreams []Upstream, responses []UpstreamResponse, aggregation Aggregation, log *zap.Logger) AggregatedResponse {
	if len(responses) == 1 {
		return a.rawResponse(responses[0])
	}

	switch aggregation.Strategy {
	case strategyMerge:
		return a.merged(responses, aggregation, log)
	case strategyArray:
		return a.arrayed(responses, aggregation, log)
	case strategyNamespace:
		return a.namespaced(upstreams, responses, aggregation, log)
	default:
		log.Error("unknown aggregation strategy", zap.String("strategy", aggregation.Strategy.String()))
		return AggregatedResponse{}
	}
}

func (a *defaultAggregator) rawResponse(response UpstreamResponse) AggregatedResponse {
	if response.Err != nil {
		return AggregatedResponse{
			Errors: []ClientError{a.mapUpstreamError(response.Err)},
		}
	}

	return AggregatedResponse{
		Data:    response.Body,
		Headers: mergeSuccessfulHeaders([]UpstreamResponse{response}),
	}
}

type field struct {
	value interface{} // Upstream response data.
	owner int         // Upstream index.
}

//nolint:gocognit // to be refactor
func (a *defaultAggregator) merged(responses []UpstreamResponse, aggregation Aggregation, log *zap.Logger) AggregatedResponse {
	merged := make(map[string]field)

	var aggregationErrors []ClientError

	hasSuccessful := false

	for idx, resp := range responses {
		var obj map[string]interface{}

		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			log.Warn(
				"upstream has errors",
				zap.Bool("best_effort", aggregation.BestEffort),
				zap.String("upstream_error", resp.Err.Unwrap().Error()),
				zap.String("client_error", clientError.String()),
			)

			if !aggregation.BestEffort {
				var allErrors []ClientError
				for _, r := range responses {
					if r.Err != nil {
						allErrors = append(allErrors, a.mapUpstreamError(r.Err))
					}
				}

				return AggregatedResponse{
					Errors: dedupeErrors(allErrors),
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)

			continue
		}

		hasSuccessful = true

		if resp.Body == nil {
			continue
		}

		if err := json.Unmarshal(resp.Body, &obj); err != nil {
			log.Error("cannot unmarshal upstream response", zap.Error(err))

			if !aggregation.BestEffort {
				return respUpstreamMalformedError
			}

			aggregationErrors = append(aggregationErrors, ClientErrUpstreamMalformed)

			continue
		}

		for k, v := range obj {
			current, exists := merged[k]

			if !exists {
				merged[k] = field{value: v, owner: idx}
				continue
			}

			switch aggregation.ConflictPolicy {
			case conflictPolicyOverwrite:
				merged[k] = field{value: v, owner: idx}
			case conflictPolicyFirst:
				// We do nothing, since the first value has already been written
			case conflictPolicyError:
				return AggregatedResponse{
					Data:    nil,
					Errors:  []ClientError{ClientErrValueConflict},
					Partial: false,
				}
			case conflictPolicyPrefer:
				// If the current owner is preferred, we do nothing
				if current.owner == aggregation.PreferredUpstream {
					continue
				}

				// If the new upstream is preferred, we replace it.
				if idx == aggregation.PreferredUpstream {
					merged[k] = field{value: v, owner: idx}
				}

				// Otherwise, we do nothing
			}
		}
	}

	final := make(map[string]interface{}, len(merged))
	for k, v := range merged {
		final[k] = v.value
	}

	data, err := json.Marshal(final)
	if err != nil {
		return respInternalError
	}

	return AggregatedResponse{
		Data:    data,
		Headers: mergeSuccessfulHeaders(responses),
		Errors:  dedupeErrors(aggregationErrors),
		Partial: len(aggregationErrors) > 0 && hasSuccessful,
	}
}

func (a *defaultAggregator) arrayed(responses []UpstreamResponse, aggregation Aggregation, log *zap.Logger) AggregatedResponse {
	var arr []json.RawMessage

	var aggregationErrors []ClientError

	hasSuccessful := false

	for _, resp := range responses {
		// Handle upstream error
		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			log.Warn(
				"upstream has errors",
				zap.Bool("best_effort", aggregation.BestEffort),
				zap.String("upstream_error", resp.Err.Unwrap().Error()),
				zap.String("client_error", clientError.String()),
			)

			if !aggregation.BestEffort {
				var allErrors []ClientError
				for _, r := range responses {
					if r.Err != nil {
						allErrors = append(allErrors, a.mapUpstreamError(r.Err))
					}
				}

				return AggregatedResponse{
					Errors: dedupeErrors(allErrors),
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)

			continue
		}

		hasSuccessful = true

		if resp.Body == nil {
			continue
		}

		arr = append(arr, resp.Body)
	}

	data, err := json.Marshal(arr)
	if err != nil {
		return respUpstreamMalformedError
	}

	aggregationResponse := AggregatedResponse{
		Data:    data,
		Headers: mergeSuccessfulHeaders(responses),
		Errors:  dedupeErrors(aggregationErrors),
		Partial: len(aggregationErrors) > 0 && hasSuccessful,
	}

	return aggregationResponse
}

func (a *defaultAggregator) namespaced(upstreams []Upstream, responses []UpstreamResponse, aggregation Aggregation, _ *zap.Logger) AggregatedResponse {
	result := make(map[string]json.RawMessage, len(responses))

	var aggregationErrors []ClientError
	hasSuccessful := false

	for i, resp := range responses {
		upstreamName := upstreams[i].Name()

		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			if !aggregation.BestEffort {
				return AggregatedResponse{
					Errors: []ClientError{a.mapUpstreamError(resp.Err)},
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)
			continue
		}

		hasSuccessful = true

		if resp.Body == nil {
			result[upstreamName] = json.RawMessage("null")
			continue
		}

		result[upstreamName] = resp.Body
	}

	data, err := json.Marshal(result)
	if err != nil {
		return respInternalError
	}

	return AggregatedResponse{
		Data:    data,
		Headers: mergeSuccessfulHeaders(responses),
		Errors:  dedupeErrors(aggregationErrors),
		Partial: len(aggregationErrors) > 0 && hasSuccessful,
	}
}

func mergeSuccessfulHeaders(responses []UpstreamResponse) http.Header {
	merged := make(http.Header)

	for _, resp := range responses {
		if resp.Err != nil {
			continue
		}

		for k, v := range resp.Headers {
			merged[k] = v
		}
	}

	return merged
}

func (a *defaultAggregator) mapUpstreamError(err *UpstreamError) ClientError {
	if err == nil {
		return ClientErrInternal
	}

	switch err.Kind { //nolint:exhaustive // will be in future releases
	case UpstreamTimeout, UpstreamConnection:
		return ClientErrUpstreamUnavailable
	case UpstreamBadStatus:
		return ClientErrUpstreamError
	case UpstreamBodyTooLarge:
		return ClientErrUpstreamBodyTooLarge
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
