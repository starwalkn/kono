package circuitbreaker

import (
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

type CircuitBreaker struct {
	mu            sync.Mutex
	state         State
	failures      int
	lastFailureAt time.Time

	maxFailures   int
	resetTimeout  time.Duration
	halfOpenTrial bool
}

func New(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:        Closed,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
	}
}

func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Open:
		if time.Since(b.lastFailureAt) >= b.resetTimeout {
			b.state = HalfOpen
			b.halfOpenTrial = false

			return true
		}

		return false
	case HalfOpen:
		if !b.halfOpenTrial {
			b.halfOpenTrial = true

			return true
		}

		return false
	default:
		return true
	}
}

func (b *CircuitBreaker) OnFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lastFailureAt = time.Now()

	switch b.state {
	case HalfOpen:
		b.state = Open
		b.failures = b.maxFailures
	case Closed:
		b.failures++

		if b.failures >= b.maxFailures {
			b.state = Open
		}
	}
}

func (b *CircuitBreaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case HalfOpen:
		b.state = Closed
		b.failures = 0
	case Closed:
		b.failures = 0
	}
}

func (b *CircuitBreaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}
