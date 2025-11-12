package tokka

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

const (
	strategyMerge = "merge"
	strategyArray = "array"
)

type aggregator interface {
	aggregate(responses [][]byte, mode string, allowPartialResults bool) []byte
}

type defaultAggregator struct {
	log *zap.Logger
}

func (a *defaultAggregator) aggregate(responses [][]byte, mode string, allowPartialResults bool) []byte {
	switch mode {
	case strategyMerge:
		res, err := a.doMerge(responses, allowPartialResults)
		if err != nil {
			a.log.Error("cannot merge responses", zap.Error(err))
			return nil
		}

		return res
	case strategyArray:
		res, err := a.doArray(responses)
		if err != nil {
			a.log.Error("cannot make array from responses", zap.Error(err))
			return nil
		}

		return res
	default:
		return nil
	}
}

func (a *defaultAggregator) doMerge(responses [][]byte, allowPartialResults bool) ([]byte, error) {
	merged := make(map[string]interface{})

	for _, resp := range responses {
		var obj map[string]interface{}
		if err := json.Unmarshal(resp, &obj); err != nil {
			if allowPartialResults {
				a.log.Warn(
					"failed to unmarshal response (partial results allowed)",
					zap.Error(err),
				)

				continue
			}

			return nil, fmt.Errorf("cannot unmarshal response (partial results not allowed): %w", err)
		}

		for k, v := range obj {
			merged[k] = v
		}
	}

	res, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal merged result: %w", err)
	}

	return res, nil
}

func (a *defaultAggregator) doArray(responses [][]byte) ([]byte, error) {
	var arr []json.RawMessage

	for _, resp := range responses {
		arr = append(arr, json.RawMessage(resp))
	}

	res, err := json.Marshal(arr)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal array result: %w", err)
	}

	return res, nil
}
