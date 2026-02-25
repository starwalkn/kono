package kono

import (
	"encoding/json"
	"errors"
	"maps"

	"go.uber.org/zap"
)

const (
	strategyMerge = "merge"
	strategyArray = "array"
)

type AggregatedResponse struct {
	Data    json.RawMessage
	Errors  []ClientError
	Partial bool
}

type aggregator interface {
	aggregate(responses []UpstreamResponse, aggregation AggregationConfig) AggregatedResponse
}

type defaultAggregator struct {
	log *zap.Logger
}

// aggregate combines multiple upstream responses based on the route's strategy.
// Single responses are returned as-is. Multiple responses are aggregated either
// by merging JSON objects ("merge") or creating a JSON array ("array").
// Upstream errors respect allowPartialResults: partial results may be included
// if allowed; otherwise a single error response is returned.
func (a *defaultAggregator) aggregate(responses []UpstreamResponse, aggregation AggregationConfig) AggregatedResponse {
	if len(responses) == 1 {
		return a.rawResponse(responses)
	}

	switch aggregation.Strategy {
	case strategyMerge:
		return a.mergeResponses(responses, aggregation.AllowPartialResults)
	case strategyArray:
		return a.arrayOfResponses(responses, aggregation.AllowPartialResults)
	default:
		a.log.Error("unknown aggregation strategy", zap.String("strategy", aggregation.Strategy))
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

func (a *defaultAggregator) mergeResponses(responses []UpstreamResponse, allowPartialResults bool) AggregatedResponse {
	merged := make(map[string]interface{})

	var aggregationErrors []ClientError

	for _, resp := range responses {
		var obj map[string]interface{}

		// Handle upstream error
		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			a.log.Warn(
				"upstream has errors",
				zap.Bool("allow_partial_results", allowPartialResults),
				zap.String("upstream_error", resp.Err.Unwrap().Error()),
				zap.String("client_error", clientError.String()),
			)

			if !allowPartialResults {
				return AggregatedResponse{
					Data:    nil,
					Errors:  []ClientError{clientError},
					Partial: false,
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)

			continue
		}

		if resp.Body == nil {
			continue
		}

		if err := json.Unmarshal(resp.Body, &obj); err != nil {
			a.log.Warn(
				"failed to unmarshal response",
				zap.Bool("allow_partial_results", allowPartialResults),
				zap.Error(err),
			)

			if !allowPartialResults {
				return getRespUpstreamMalformedError()
			}

			aggregationErrors = append(aggregationErrors, ClientErrUpstreamMalformed)

			continue
		}

		maps.Copy(merged, obj)
	}

	data, err := json.Marshal(merged)
	if err != nil {
		return getRespInternalError()
	}

	aggregationResponse := AggregatedResponse{
		Data:    data,
		Errors:  dedupeErrors(aggregationErrors),
		Partial: len(aggregationErrors) > 0,
	}

	return aggregationResponse
}

func (a *defaultAggregator) arrayOfResponses(responses []UpstreamResponse, allowPartialResults bool) AggregatedResponse {
	var arr []json.RawMessage

	var aggregationErrors []ClientError

	for _, resp := range responses {
		// Handle upstream error
		if resp.Err != nil {
			clientError := a.mapUpstreamError(resp.Err)

			a.log.Warn(
				"upstream has errors",
				zap.Bool("allow_partial_results", allowPartialResults),
				zap.String("upstream_error", resp.Err.Unwrap().Error()),
				zap.String("client_error", clientError.String()),
			)

			if !allowPartialResults {
				return AggregatedResponse{
					Data:    nil,
					Errors:  []ClientError{clientError},
					Partial: false,
				}
			}

			aggregationErrors = append(aggregationErrors, clientError)

			continue
		}

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
		Partial: len(aggregationErrors) > 0,
	}

	return aggregationResponse
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
