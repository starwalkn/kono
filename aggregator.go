package kono

import (
	"encoding/json"
	"errors"

	"go.uber.org/zap"
)

type AggregatedResponse struct {
	Data    json.RawMessage
	Errors  []ClientError
	Partial bool
}

type aggregator interface {
	aggregate(responses []UpstreamResponse, aggregation Aggregation) AggregatedResponse
}

type defaultAggregator struct {
	log *zap.Logger
}

// aggregate combines multiple upstream responses based on the route's strategy.
// Single responses are returned as-is. Multiple responses are aggregated either
// by merging JSON objects ("merge") or creating a JSON array ("array").
// Upstream errors respect bestEffort: partial results may be included
// if allowed; otherwise a single error response is returned.
func (a *defaultAggregator) aggregate(responses []UpstreamResponse, aggregation Aggregation) AggregatedResponse {
	if len(responses) == 1 {
		return a.rawResponse(responses)
	}

	switch aggregation.Strategy {
	case strategyMerge:
		return a.merged(responses, aggregation)
	case strategyArray:
		return a.arrayed(responses, aggregation)
	case strategyNamespace:
		return a.namespaced(responses, aggregation)
	default:
		a.log.Error("unknown aggregation strategy", zap.String("strategy", aggregation.Strategy.String()))
		return AggregatedResponse{}
	}
}

func (a *defaultAggregator) rawResponse(responses []UpstreamResponse) AggregatedResponse {
	if len(responses) > 1 {
		return getRespInternalError()
	}

	resp := responses[0]
	if resp.Err != nil {
		return AggregatedResponse{
			Data:    nil,
			Errors:  []ClientError{a.mapUpstreamError(resp.Err)},
			Partial: false,
		}
	}

	if resp.Body == nil {
		return AggregatedResponse{}
	}

	return AggregatedResponse{
		Data:    resp.Body,
		Errors:  nil,
		Partial: false,
	}
}

type field struct {
	value interface{} // Upstream response data.
	owner int         // Upstream index.
}

func (a *defaultAggregator) merged(responses []UpstreamResponse, aggregation Aggregation) AggregatedResponse {
	merged := make(map[string]field)

	var aggregationErrors []ClientError

	successfulResponseCount := 0

	for idx, resp := range responses {
		var obj map[string]interface{}

		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			a.log.Warn(
				"upstream has errors",
				zap.Bool("best_effort", aggregation.BestEffort),
				zap.String("upstream_error", resp.Err.Error()),
				zap.String("client_error", clientError.String()),
			)

			if !aggregation.BestEffort {
				return AggregatedResponse{
					Data:    nil,
					Errors:  []ClientError{clientError},
					Partial: false,
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)

			continue
		}

		successfulResponseCount++

		if resp.Body == nil {
			continue
		}

		if err := json.Unmarshal(resp.Body, &obj); err != nil {
			if !aggregation.BestEffort {
				return getRespUpstreamMalformedError()
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
		return getRespInternalError()
	}

	return AggregatedResponse{
		Data:    data,
		Errors:  dedupeErrors(aggregationErrors),
		Partial: len(aggregationErrors) > 0 && successfulResponseCount > 0,
	}
}

func (a *defaultAggregator) arrayed(responses []UpstreamResponse, aggregation Aggregation) AggregatedResponse {
	var arr []json.RawMessage

	var aggregationErrors []ClientError

	successfulResponseCount := 0

	for _, resp := range responses {
		// Handle upstream error
		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			a.log.Warn(
				"upstream has errors",
				zap.Bool("best_effort", aggregation.BestEffort),
				zap.String("upstream_error", resp.Err.Unwrap().Error()),
				zap.String("client_error", clientError.String()),
			)

			if !aggregation.BestEffort {
				return AggregatedResponse{
					Data:    nil,
					Errors:  []ClientError{clientError},
					Partial: false,
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)

			continue
		}

		successfulResponseCount++

		if resp.Body == nil {
			continue
		}

		arr = append(arr, resp.Body)
	}

	data, err := json.Marshal(arr)
	if err != nil {
		return getRespUpstreamMalformedError()
	}

	aggregationResponse := AggregatedResponse{
		Data:    data,
		Errors:  dedupeErrors(aggregationErrors),
		Partial: len(aggregationErrors) > 0 && successfulResponseCount > 0,
	}

	return aggregationResponse
}

func (a *defaultAggregator) namespaced(_ []UpstreamResponse, _ Aggregation) AggregatedResponse {
	return AggregatedResponse{
		Data:    nil,
		Errors:  []ClientError{"not implemented"},
		Partial: false,
	}
}

func (a *defaultAggregator) mapUpstreamError(err error) ClientError {
	var ue *UpstreamError

	if !errors.As(err, &ue) {
		return ClientErrInternal
	}

	switch ue.Kind { //nolint:exhaustive // will be in future releases
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

func getRespInternalError() AggregatedResponse {
	return AggregatedResponse{
		Data:    nil,
		Errors:  []ClientError{ClientErrInternal},
		Partial: false,
	}
}

func getRespUpstreamMalformedError() AggregatedResponse {
	return AggregatedResponse{
		Data:    nil,
		Errors:  []ClientError{ClientErrUpstreamMalformed},
		Partial: false,
	}
}

func dedupeErrors(errs []ClientError) []ClientError {
	seen := make(map[ClientError]struct{})
	out := make([]ClientError, 0, len(errs))

	for _, e := range errs {
		if _, ok := seen[e]; ok {
			continue
		}

		seen[e] = struct{}{}
		out = append(out, e)
	}

	return out
}
