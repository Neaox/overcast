package groups_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Neaox/overcast-compat-cli/internal/groups"
	"github.com/Neaox/overcast-compat-cli/internal/registry"
)

// The suite's own registrations must resolve against the real registry.json.
//
// This is the check that catches a mis-binding before a run reports one: an
// unresolvable key, or a bare key for a name that several groups declare and
// which therefore binds an arbitrary one of them. Running it as a unit test
// means the failure lands in `go test` rather than in a compat run whose
// results silently describe the wrong test.
func TestRegisteredImplsResolveAgainstRegistry(t *testing.T) {
	t.Setenv("OVERCAST_REGISTRY_PATH", filepath.Join("..", "..", "..", "registry.json"))
	if _, err := os.Stat(os.Getenv("OVERCAST_REGISTRY_PATH")); err != nil {
		t.Fatalf("registry.json not readable from the test working directory: %v", err)
	}

	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	impls := registry.ImplMap{}
	for _, svc := range groups.All() {
		for k, v := range svc.Impls {
			impls[k] = v
		}
	}

	if err := registry.ValidateImpls(reg, impls, "cli"); err != nil {
		t.Fatalf("cli impl registrations are not resolvable:\n%v", err)
	}
}
