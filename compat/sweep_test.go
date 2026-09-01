package compat

import (
	"slices"
	"testing"
	"time"
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

func TestSettledLeakedContainers_outwaitsAsyncRemoval(t *testing.T) {
	// Given: a daemon still showing a run's ECS pause containers on the first
	// look — deletes stop containers before returning, but hand the removal to
	// the emulator's background GC — and showing none a moment later, once the
	// GC's queue has been worked. This is the state a whole-suite run of a few
	// seconds ends in on every run, and what the audit kept reporting as an
	// emulator leak.
	calls := 0
	list := func() ([]string, error) {
		calls++
		if calls == 1 {
			return []string{
				"overcast-ecs-compat-svc-oc-29c6b98a-go-4469c995-internal.ecs.pause",
				"overcast-ecs-compat-svc-oc-29c6b98a-go-8240360f-internal.ecs.pause",
				"overcast-ecs-compat-svc-oc-29c6b98a-go-85cc358c-internal.ecs.pause",
			}, nil
		}
		return nil, nil
	}

	// When: the audit is given a grace period.
	got := settledLeakedContainers(t.Context(), "oc-29c6b98a", list, time.Second, time.Millisecond)

	// Then: nothing is reported — a container mid-removal is teardown in
	// flight, not a leak.
	if len(got) != 0 {
		t.Fatalf("leaked = %#v, want none — these containers were removed within the grace period", got)
	}
}

func TestSettledLeakedContainers_reportsWhatSurvivesTheGrace(t *testing.T) {
	// Given: one container the daemon keeps answering with — a leak in earnest —
	// alongside one that is removed after the first look.
	calls := 0
	list := func() ([]string, error) {
		calls++
		names := []string{"overcast-lambda-oc-29c6b98a-go-fninvoke-1788236672118203688"}
		if calls == 1 {
			names = append(names, "overcast-ecs-compat-svc-oc-29c6b98a-go-4469c995-internal.ecs.pause")
		}
		return names, nil
	}

	// When: the grace period runs out.
	got := settledLeakedContainers(t.Context(), "oc-29c6b98a", list, 30*time.Millisecond, 5*time.Millisecond)

	// Then: only the survivor is reported.
	want := []string{"overcast-lambda-oc-29c6b98a-go-fninvoke-1788236672118203688"}
	if !slices.Equal(got, want) {
		t.Fatalf("leaked = %#v, want %#v", got, want)
	}
}

func TestSettledLeakedContainers_cleanFirstLookPollsNothing(t *testing.T) {
	// Given: a daemon with nothing of the run's on it.
	calls := 0
	list := func() ([]string, error) {
		calls++
		return []string{"compat-overcast-1"}, nil
	}

	// When/Then: the audit answers from the one listing — the common case must
	// not slow the run down by a grace period it has no use for.
	if got := settledLeakedContainers(t.Context(), "oc-12345678", list, time.Minute, time.Millisecond); len(got) != 0 {
		t.Fatalf("leaked = %#v, want none", got)
	}
	if calls != 1 {
		t.Fatalf("list called %d times, want 1 — a clean first look needs no polling", calls)
	}
}
