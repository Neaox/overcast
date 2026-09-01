package serviceutil_test

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

type testRec struct {
	Name string `json:"Name"`
}

func seedRegionScanStore(t *testing.T) state.Store {
	t.Helper()
	st := state.NewMemoryStore()
	ctx := context.Background()
	for key, name := range map[string]string{
		"us-east-1/a": "a-us",
		"eu-west-1/a": "a-eu",
		"eu-west-1/b": "b-eu",
		"legacy":      "legacy", // no region prefix — attributed to the default region
	} {
		if err := st.Set(ctx, "test:ns", key, `{"Name":"`+name+`"}`); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	return st
}

func TestScanRegions(t *testing.T) {
	st := seedRegionScanStore(t)
	got, err := serviceutil.ScanRegions[testRec](context.Background(), st, "test:ns", "us-east-1")
	if err != nil {
		t.Fatalf("ScanRegions: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ScanRegions returned %d records, want 4", len(got))
	}
	byName := make(map[string]string, len(got))
	for _, r := range got {
		byName[r.Value.Name] = r.Region
	}
	want := map[string]string{"a-us": "us-east-1", "a-eu": "eu-west-1", "b-eu": "eu-west-1", "legacy": "us-east-1"}
	for name, region := range want {
		if byName[name] != region {
			t.Errorf("record %q attributed to region %q, want %q", name, byName[name], region)
		}
	}
}

func TestFindRegioned(t *testing.T) {
	st := seedRegionScanStore(t)
	ctx := context.Background()

	rec, region, found, err := serviceutil.FindRegioned[testRec](ctx, st, "test:ns", "b", "us-east-1")
	if err != nil || !found {
		t.Fatalf("FindRegioned(b): found=%v err=%v", found, err)
	}
	if rec.Name != "b-eu" || region != "eu-west-1" {
		t.Fatalf("FindRegioned(b) = (%q, %q), want (b-eu, eu-west-1)", rec.Name, region)
	}

	if _, _, found, err := serviceutil.FindRegioned[testRec](ctx, st, "test:ns", "missing", "us-east-1"); err != nil || found {
		t.Fatalf("FindRegioned(missing): found=%v err=%v, want found=false", found, err)
	}
}
