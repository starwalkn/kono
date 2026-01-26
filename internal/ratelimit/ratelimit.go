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

func New(cfg map[string]any) *RateLimit {
	return &RateLimit{
		limit:   intFrom(cfg, "limit", defaultLimit),
		window:  time.Duration(intFrom(cfg, "window", int(defaultWindow.Seconds()))) * time.Second,
		buckets: make(map[string]*entry),
		stopCh:  make(chan struct{}),
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
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &entry{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	if b.count < rl.limit {
		b.count++
		return true
	}

	return false
}

func (rl *RateLimit) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, b := range rl.buckets {
		if now.After(b.resetAt) {
			delete(rl.buckets, key)
		}
	}
}

func intFrom(cfg map[string]any, key string, def int) int {
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
