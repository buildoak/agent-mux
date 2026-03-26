package adapter

import (
	"fmt"
	"sort"

	"github.com/buildoak/agent-mux/internal/types"
)

type Registry struct {
	adapters map[string]types.HarnessAdapter
	models   map[string][]string
}

func NewRegistry(models map[string][]string) *Registry {
	r := &Registry{
		adapters: make(map[string]types.HarnessAdapter),
		models:   models,
	}
	r.Register("codex", &CodexAdapter{})
	r.Register("claude", &ClaudeAdapter{})
	r.Register("gemini", &GeminiAdapter{})
	return r
}

// Register adds an adapter by name.
func (r *Registry) Register(name string, adp types.HarnessAdapter) {
	r.adapters[name] = adp
}

// Get returns the adapter for the given engine name, or an error with suggestions.
func (r *Registry) Get(name string) (types.HarnessAdapter, error) {
	adp, ok := r.adapters[name]
	if !ok {
		engines := r.ValidEngines()
		return nil, fmt.Errorf("engine %q not found. Valid engines: %v", name, engines)
	}
	return adp, nil
}

// ValidEngines returns sorted list of registered engine names.
func (r *Registry) ValidEngines() []string {
	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidModels returns the model list for the given engine from config.
// Returns nil if engine has no configured models.
func (r *Registry) ValidModels(engine string) []string {
	if r.models == nil {
		return nil
	}
	return r.models[engine]
}
