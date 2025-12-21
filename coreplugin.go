package tokka

import "sync"

type CorePlugin interface {
	Name() string
	Init(cfg map[string]any) error
	Start() error
	Stop() error
}

//nolint:gochecknoglobals // non concurrently uses
var (
	coreRegistry = make(map[string]func() CorePlugin)
	activeCores  = make(map[string]CorePlugin)
	muCores      sync.RWMutex
)

// RegisterCorePlugin adds core plugin factory to registry.
// This function should only be used by core plugin implementations.
func RegisterCorePlugin(name string, factory func() CorePlugin) {
	coreRegistry[name] = factory
}

func createCorePlugin(name string) CorePlugin {
	if f, ok := coreRegistry[name]; ok {
		return f()
	}

	return nil
}

func registerActiveCorePlugin(name string, plugin CorePlugin) {
	muCores.Lock()
	defer muCores.Unlock()
	activeCores[name] = plugin
}

func getActiveCorePlugin(name string) CorePlugin {
	muCores.RLock()
	defer muCores.RUnlock()

	return activeCores[name]
}
