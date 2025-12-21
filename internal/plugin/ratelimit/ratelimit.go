package ratelimit

import (
	"sync"
	"time"

	"github.com/starwalkn/tokka"
)

func init() {
	tokka.RegisterCorePlugin("ratelimit", NewPlugin)
}

const (
	defaultLimit  = 60
	defaultWindow = 60 * time.Second
	cleanupEvery  = 10 * time.Second
)

type entry struct {
	count   int
	resetAt time.Time
}

type Plugin struct {
	limit   int
	window  time.Duration
	mu      sync.Mutex
	buckets map[string]*entry

	stopCh  chan struct{}
	stopped bool
}

func NewPlugin() tokka.CorePlugin {
	return &Plugin{}
}

func (p *Plugin) Name() string { return "ratelimit" }

func (p *Plugin) Init(cfg map[string]any) error {
	p.limit = intFrom(cfg, "limit", defaultLimit)
	p.window = time.Duration(intFrom(cfg, "window", int(defaultWindow.Seconds()))) * time.Second
	p.buckets = make(map[string]*entry)
	p.stopCh = make(chan struct{})

	return nil
}

func (p *Plugin) Start() error {
	go func() {
		ticker := time.NewTicker(cleanupEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.cleanup()
			case <-p.stopCh:
				return
			}
		}
	}()
	return nil
}

func (p *Plugin) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped {
		return nil
	}

	close(p.stopCh)
	p.stopped = true
	return nil
}

func (p *Plugin) Allow(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	b, ok := p.buckets[key]
	if !ok || now.After(b.resetAt) {
		p.buckets[key] = &entry{
			count:   1,
			resetAt: now.Add(p.window),
		}
		return true
	}

	if b.count < p.limit {
		b.count++
		return true
	}

	return false
}

func (p *Plugin) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for key, b := range p.buckets {
		if now.After(b.resetAt) {
			delete(p.buckets, key)
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
