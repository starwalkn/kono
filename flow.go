package kono

type Flow struct {
	Path                 string
	Method               string
	Aggregation          Aggregation
	MaxParallelUpstreams int64
	Upstreams            []Upstream
	Scripts              []Script
	Plugins              []Plugin
	Middlewares          []Middleware
}

type Aggregation struct {
	BestEffort        bool
	Strategy          aggregationStrategy
	ConflictPolicy    conflictPolicy // Conflict policy be set only for 'merge' aggregation strategy.
	PreferredUpstream int            // Preferred upstream used only for 'prefer' conflict policy.
}

type aggregationStrategy uint8

func (s aggregationStrategy) String() string {
	switch s {
	case strategyArray:
		return "array"
	case strategyMerge:
		return "merge"
	case strategyNamespace:
		return "namespace"
	default:
		return "unknown"
	}
}

type conflictPolicy uint8

func (c conflictPolicy) String() string {
	switch c {
	case conflictPolicyOverwrite:
		return "overwrite"
	case conflictPolicyError:
		return "error"
	case conflictPolicyFirst:
		return "first"
	case conflictPolicyPrefer:
		return "prefer"
	default:
		return "unknown"
	}
}

const (
	strategyMerge aggregationStrategy = iota
	strategyArray
	strategyNamespace
)

const (
	conflictPolicyOverwrite conflictPolicy = iota
	conflictPolicyError
	conflictPolicyFirst
	conflictPolicyPrefer
)
