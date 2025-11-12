package tokka

import "sync"

type RateLimit interface {
	Allow(key string) bool
}

type CorePlugin interface {
	Name() string
	Init(cfg map[string]interface{}) error
	Start() error
	Stop() error
}

//nolint:gochecknoglobals // non concurrently uses
var (
	coreRegistry = make(map[string]func() CorePlugin)
	activeCores  = make(map[string]CorePlugin)
	muCores      sync.RWMutex
)

func RegisterCorePlugin(name string, factory func() CorePlugin) {
	coreRegistry[name] = factory
}

func createCorePlugin(name string) CorePlugin {
	if f, ok := coreRegistry[name]; ok {
		return f()
	}
	return nil
}

func RegisterActiveCore(name string, plugin CorePlugin) {
	muCores.Lock()
	defer muCores.Unlock()
	activeCores[name] = plugin
}

func GetCorePlugin(name string) CorePlugin {
	muCores.RLock()
	defer muCores.RUnlock()
	return activeCores[name]
}

func ActiveCores() map[string]CorePlugin {
	muCores.RLock()
	defer muCores.RUnlock()
	c := make(map[string]CorePlugin, len(activeCores))
	for k, v := range activeCores {
		c[k] = v
	}
	return c
}
