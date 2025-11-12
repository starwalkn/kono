package metrics

import (
	"fmt"
	"sync"
	"time"

	"github.com/starwalkn/tokka"
)

func init() {
	tokka.RegisterCorePlugin("metrics", NewPlugin)
}

type Plugin struct {
	mu     sync.Mutex
	counts map[string]int
	ticker *time.Ticker
	stopCh chan struct{}
}

func NewPlugin() tokka.CorePlugin {
	return &Plugin{
		counts: make(map[string]int),
		stopCh: make(chan struct{}),
	}
}

func (p *Plugin) Name() string {
	return "core_metrics"
}

func (p *Plugin) Init(cfg map[string]interface{}) error {
	intervalSec, ok := cfg["log_interval_sec"].(float64)
	if !ok || intervalSec <= 0 {
		intervalSec = 10
	}
	p.ticker = time.NewTicker(time.Duration(intervalSec) * time.Second)
	return nil
}

func (p *Plugin) Start() error {
	go func() {
		for {
			select {
			case <-p.ticker.C:
				p.mu.Lock()
				for path, count := range p.counts {
					//nolint:forbidigo // stub implementation
					fmt.Printf("%s: %d\n", path, count)
				}
				p.mu.Unlock()
			case <-p.stopCh:
				return
			}
		}
	}()
	return nil
}

func (p *Plugin) Stop() error {
	close(p.stopCh)
	if p.ticker != nil {
		p.ticker.Stop()
	}
	return nil
}

func (p *Plugin) Record(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[path]++
}
