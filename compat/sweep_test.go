package compat

import (
	"slices"
	"testing"
)

func TestLeakedContainerNames_matchesRunAndCompatMarkers(t *testing.T) {
	// Given: the daemon's view after a run whose resources were all deleted.
	// The elasticache and lambda names are real leaks captured on 2026-08-01 —
	// eleven redis containers and an execution environment outlived their
	// deleted resources.
	names := []string{
		"overcast-elasticache-rg-compat-rg-oc-70ec22c8-nj",
		"overcast-elasticache-rg-compat-rg-desc-1785535133",
		"overcast-lambda-oc-70ec22c8-go-fninvoke-1785535089766989780",
		"compat-overcast-1",      // the emulator under test (compose) — not ours to touch
		"compat-dashboard-1",     // runner infrastructure
		"overcast-toast-preview", // an unrelated overcast container without markers
		"redis-local",            // unrelated user container
	}

	// When: filtered for this run
	got := leakedContainerNames(names, "oc-70ec22c8")

	// Then: exactly the emulator-owned, marker-carrying containers are named —
	// including the one whose resource name lacked the run prefix, which the
	// generic marker catches — and infrastructure and user containers are not.
	want := []string{
		"overcast-elasticache-rg-compat-rg-desc-1785535133",
		"overcast-elasticache-rg-compat-rg-oc-70ec22c8-nj",
		"overcast-lambda-oc-70ec22c8-go-fninvoke-1785535089766989780",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("leaked = %#v, want %#v", got, want)
	}
}

func TestLeakedContainerNames_emptyRunIDStillCatchesMarkers(t *testing.T) {
	// Given: no run ID (defensive) — the generic markers still apply
	got := leakedContainerNames([]string{
		"overcast-elasticache-compat-leak",
		"overcast-rds-oc-deadbeef-db",
	}, "")
	want := []string{"overcast-elasticache-compat-leak", "overcast-rds-oc-deadbeef-db"}
	if !slices.Equal(got, want) {
		t.Fatalf("leaked = %#v, want %#v", got, want)
	}
}

func TestLeakedContainerNames_nothingLeakedIsQuiet(t *testing.T) {
	// Given: only infrastructure and unrelated containers
	got := leakedContainerNames([]string{"compat-overcast-1", "buildx_buildkit_x", "redis-local"}, "oc-12345678")
	if len(got) != 0 {
		t.Fatalf("leaked = %#v, want none", got)
	}
}
