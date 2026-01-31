package ratelimit

import (
	"sync"
	"time"
)

const (
	defaultLimit  = 60
	defaultWindow = 60 * time.Second
	cleanupEvery  = 10 * time.Second
)

type entry struct {
	count   int
	resetAt time.Time
}

type RateLimit struct {
	limit   int
	window  time.Duration
	mu      sync.Mutex
	buckets map[string]*entry

	stopCh  chan struct{}
	stopped bool
}

func New(cfg map[string]interface{}) *RateLimit {
	window, err := time.ParseDuration(cfg["window"].(string))
	if err != nil {
		window = defaultWindow
	}

	return &RateLimit{
		limit:   intFrom(cfg, "limit", defaultLimit),
		window:  window,
		mu:      sync.Mutex{},
		buckets: make(map[string]*entry),

		stopCh:  make(chan struct{}),
		stopped: false,
	}
}

func (rl *RateLimit) Start() error {
	go func() {
		ticker := time.NewTicker(cleanupEvery)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				rl.cleanup()
			case <-rl.stopCh:
				return
			}
		}
	}()

	return nil
}

func (rl *RateLimit) Stop() error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.stopped {
		return nil
	}

	close(rl.stopCh)
	rl.stopped = true

	return nil
}

func (rl *RateLimit) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	ent, ok := rl.buckets[key]
	if !ok || now.After(ent.resetAt) {
		// New window
		reset := now.Add(rl.window)

		rl.buckets[key] = &entry{
			count:   1,
			resetAt: reset,
		}

		return true
	}

	if ent.count < rl.limit {
		ent.count++
		return true
	}

	return false
}

func (rl *RateLimit) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	for key, ent := range rl.buckets {
		if now.After(ent.resetAt) {
			delete(rl.buckets, key)
		}
	}
}

func intFrom(cfg map[string]interface{}, key string, def int) int {
	if v, ok := cfg[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		}
	}

	return def
}
