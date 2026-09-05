package groups_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/groups"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/registry"
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

	impls, err := mergeAll(t)
	if err != nil {
		t.Fatalf("go-sdk impl registrations collide:\n%v", err)
	}

	if err := registry.ValidateImpls(reg, impls, "go-sdk"); err != nil {
		t.Fatalf("go-sdk impl registrations are not resolvable:\n%v", err)
	}
}

// No two service files may register the same impl key. The merge is
// last-writer-wins, so a collision discards one implementation and the test it
// belonged to silently runs the other file's — exactly what the resolution
// rules above prevent for registry lookups, one step earlier in the pipeline.
func TestRegisteredImplsHaveNoDuplicateKeys(t *testing.T) {
	if _, err := mergeAll(t); err != nil {
		t.Fatalf("go-sdk service files register overlapping impl keys:\n%v", err)
	}
}

// Every impl key must be group-qualified ("group:test"). #1700: a bare key
// works only while its test name happens to be unique across every registry
// group, and a future generated group can make it ambiguous at any time —
// silently discarding the impl (BuildGroups refuses the bare fallback for an
// ambiguous name) rather than failing to compile. Qualifying every key removes
// that landmine instead of waiting to trip over it.
func TestRegisteredImplsHaveNoBareKeys(t *testing.T) {
	svcGroups := groups.All(clients.New("http://127.0.0.1:1", "us-east-1"))
	for _, svc := range svcGroups {
		for key := range svc.Impls {
			if !strings.Contains(key, ":") {
				t.Errorf("service %q registers bare impl key %q; want it qualified as %q", svc.Name, key, "<group>:"+key)
			}
		}
	}
}

// mergeAll flattens every service file's registrations the same way the runner
// does, so both tests see exactly the map a real run would build.
func mergeAll(t *testing.T) (registry.ImplMap, error) {
	t.Helper()
	svcGroups := groups.All(clients.New("http://127.0.0.1:1", "us-east-1"))
	sources := make([]registry.ImplSource, 0, len(svcGroups))
	for _, svc := range svcGroups {
		if svc.Name == "" {
			t.Errorf("service group with %d impls has no Name; a collision could not name it", len(svc.Impls))
		}
		sources = append(sources, registry.ImplSource{Name: svc.Name, Impls: svc.Impls})
	}
	return registry.MergeImpls(sources, "go-sdk")
}
