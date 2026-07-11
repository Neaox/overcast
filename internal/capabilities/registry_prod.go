//go:build !dev

package capabilities

// Registry is an empty no-op stub in production builds.
// All methods are inlineable; the linker eliminates any dead call sites entirely.
type Registry struct{}

// Default is the global capability registry (empty stub in production builds).
var Default = &Registry{}

// NewRegistry returns an empty no-op Registry stub.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(_ ...Capability)                     {}
func (r *Registry) RegisterForService(_ string, _ ...Capability) {}
func (r *Registry) All() []Capability                            { return nil }
func (r *Registry) ForService(_ string) []Capability             { return nil }
func (r *Registry) Lookup(_, _ string) (Capability, bool)        { return Capability{}, false }
