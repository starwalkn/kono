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

	threshold     int
	resetTimeout  time.Duration
	halfOpenTrial bool
}

func New(threshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:         Closed,
		failures:      0,
		threshold:     threshold,
		resetTimeout:  resetTimeout,
		halfOpenTrial: false,
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
	case Closed:
		return true
	}

	return true
}

func (b *CircuitBreaker) OnFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lastFailureAt = time.Now()

	switch b.state {
	case HalfOpen:
		b.state = Open
		b.failures = b.threshold
	case Closed:
		b.failures++

		if b.failures >= b.threshold {
			b.state = Open
		}
	case Open:
		// Already open — lastFailureAt is updated above, nothing else to do.
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
	case Open:
		// Success while open shouldn't happen — Allow() returns false for Open state.
	}
}

func (b *CircuitBreaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}
