//go:build dev

package capabilities

import "sync"

// Registry holds all declared capabilities, indexed by "service/operation".
type Registry struct {
	mu  sync.RWMutex
	all []Capability
	idx map[string]int // "service/operation" -> index into all
}

// Default is the global capability registry populated by per-service init() functions.
// Only available in dev builds; production builds use the no-op stub in registry_prod.go.
var Default = NewRegistry()

// NewRegistry returns an initialised, empty Registry.
func NewRegistry() *Registry {
	return &Registry{idx: make(map[string]int)}
}

// Register adds or updates capabilities in the registry.
// Safe for concurrent use from multiple init() functions.
func (r *Registry) Register(caps ...Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idx == nil {
		r.idx = make(map[string]int)
	}
	for _, c := range caps {
		key := c.Service + "/" + c.Operation
		if i, ok := r.idx[key]; ok {
			r.all[i] = c
		} else {
			r.idx[key] = len(r.all)
			r.all = append(r.all, c)
		}
	}
}

// RegisterForService is like Register but fills in the Service field on every
// entry before registering, so per-entry Capability literals can omit Service.
// Existing non-empty Service fields are left unchanged.
func (r *Registry) RegisterForService(service string, caps ...Capability) {
	for i := range caps {
		if caps[i].Service == "" {
			caps[i].Service = service
		}
	}
	r.Register(caps...)
}

// All returns a copy of all registered capabilities.
func (r *Registry) All() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, len(r.all))
	copy(out, r.all)
	return out
}

// ForService returns all capabilities for the given service name.
func (r *Registry) ForService(service string) []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Capability
	for _, c := range r.all {
		if c.Service == service {
			out = append(out, c)
		}
	}
	return out
}

// Lookup returns the capability for (service, operation), or false if not declared.
func (r *Registry) Lookup(service, operation string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.idx == nil {
		return Capability{}, false
	}
	i, ok := r.idx[service+"/"+operation]
	if !ok {
		return Capability{}, false
	}
	return r.all[i], true
}
