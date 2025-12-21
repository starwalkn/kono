package tokka

import (
	"encoding/json"
	"fmt"
	"maps"

	"go.uber.org/zap"
)

const (
	strategyMerge = "merge"
	strategyArray = "array"
)

type aggregator interface {
	aggregate(responses []UpstreamResponse, mode string, allowPartialResults bool) []byte
}

type defaultAggregator struct {
	log *zap.Logger
}

func (a *defaultAggregator) aggregate(responses []UpstreamResponse, mode string, allowPartialResults bool) []byte {
	switch mode {
	case strategyMerge:
		res, err := a.doMerge(responses, allowPartialResults)
		if err != nil {
			a.log.Error("cannot merge responses", zap.Error(err))
			return nil
		}

		return res
	case strategyArray:
		res, err := a.doArray(responses, allowPartialResults)
		if err != nil {
			a.log.Error("cannot make array from responses", zap.Error(err))
			return nil
		}

		return res
	default:
		a.log.Error("unknown aggregation strategy", zap.String("strategy", mode))
		return nil
	}
}

func (a *defaultAggregator) doMerge(responses []UpstreamResponse, allowPartialResults bool) ([]byte, error) {
	merged := make(map[string]any)

	for _, resp := range responses {
		var obj map[string]any

		if resp.Body == nil {
			continue
		}

		if resp.Err != nil {
			if allowPartialResults {
				a.log.Warn(
					"failed to unmarshal response",
					zap.Bool("allow_partial_results", allowPartialResults),
					zap.Error(resp.Err),
				)
			} else {
				return nil, fmt.Errorf("one or more responses failed: %w", resp.Err)
			}
		}

		if err := json.Unmarshal(resp.Body, &obj); err != nil || resp.Err != nil {
			if allowPartialResults {
				a.log.Warn(
					"failed to unmarshal response",
					zap.Bool("allow_partial_results", allowPartialResults),
					zap.Error(err),
				)

				continue
			}

			return nil, fmt.Errorf("cannot unmarshal response: %w", err)
		}

		maps.Copy(merged, obj)
	}

	res, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal merged result: %w", err)
	}

	return res, nil
}

func (a *defaultAggregator) doArray(responses []UpstreamResponse, allowPartialResults bool) ([]byte, error) {
	var arr []json.RawMessage

	for _, resp := range responses {
		if resp.Body == nil {
			continue
		}

		if resp.Err != nil {
			if allowPartialResults {
				a.log.Warn(
					"failed to unmarshal response",
					zap.Bool("allow_partial_results", allowPartialResults),
					zap.Error(resp.Err),
				)

				continue
			}

			return nil, fmt.Errorf("one or more responses failed: %w", resp.Err)
		}

		arr = append(arr, resp.Body)
	}

	res, err := json.Marshal(arr)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal array result: %w", err)
	}

	return res, nil
}
