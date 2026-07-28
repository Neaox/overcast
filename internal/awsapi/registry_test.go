package awsapi

import "testing"

func TestRegistryClaimTarget_modeledJSONOperation(t *testing.T) {
	// Given: the immutable registry generated from the pinned model corpus.
	registry := NewRegistry()

	// When: a modeled, currently unimplemented AWS JSON target is looked up.
	claim, ok := registry.ClaimTarget("GameLift.ListBuilds")

	// Then: the registry selects the JSON 501 envelope without scanning models.
	if !ok {
		t.Fatal("ClaimTarget() did not recognize GameLift.ListBuilds")
	}
	if claim.ModelService != "gamelift" || claim.Operation != "ListBuilds" {
		t.Errorf("ClaimTarget() = %+v, want gamelift ListBuilds", claim)
	}
	if claim.ErrorProfile != ErrorProfileJSON {
		t.Errorf("ClaimTarget() error profile = %v, want JSON", claim.ErrorProfile)
	}
}

func TestRegistryClaimQuery_ec2Operation(t *testing.T) {
	// Given: the immutable registry generated from the pinned model corpus.
	registry := NewRegistry()

	// When: a fully qualified EC2 Query operation is looked up.
	claim, ok := registry.ClaimQuery("2016-11-15", "DescribeInstances")

	// Then: it chooses EC2's distinct XML error envelope.
	if !ok {
		t.Fatal("ClaimQuery() did not recognize EC2 DescribeInstances")
	}
	if claim.ErrorProfile != ErrorProfileEC2QueryXML {
		t.Errorf("ClaimQuery() error profile = %v, want EC2 Query XML", claim.ErrorProfile)
	}
}

func TestRegistryClaimQuery_unknownOperation(t *testing.T) {
	// Given: the immutable registry generated from the pinned model corpus.
	registry := NewRegistry()

	// When: an unknown future operation is looked up.
	_, ok := registry.ClaimQuery("2099-01-01", "FutureQueryOperation")

	// Then: it remains unclaimed for the generic Query fallback.
	if ok {
		t.Fatal("ClaimQuery() unexpectedly claimed an unknown operation")
	}
}

func TestRegistryClaimTarget_collidingService(t *testing.T) {
	// Given: a modeled target shared by CloudWatch Events and EventBridge.
	registry := NewRegistry()

	// When: its immutable registry entry is claimed.
	claim, ok := registry.ClaimTarget("AWSEvents.CreateEventBus")

	// Then: the shared wire operation is usable for its error profile but is
	// deliberately not attributed to an arbitrary service identity.
	if !ok {
		t.Fatal("ClaimTarget() did not recognize AWSEvents.CreateEventBus")
	}
	if !claim.Ambiguous || claim.ModelService != "" || claim.Service != "" {
		t.Errorf("ClaimTarget() = %+v, want an unassigned ambiguous claim", claim)
	}
}

func TestRegistryClaimQuery_collidingService(t *testing.T) {
	// Given: a Query key shared by DocumentDB, Neptune, and RDS.
	registry := NewRegistry()

	// When: its immutable registry entry is claimed.
	claim, ok := registry.ClaimQuery("2014-10-31", "CreateDBCluster")

	// Then: the shared wire operation keeps its Query error profile without
	// inventing an owner that would corrupt later coverage reporting.
	if !ok {
		t.Fatal("ClaimQuery() did not recognize CreateDBCluster")
	}
	if !claim.Ambiguous || claim.ModelService != "" || claim.Service != "" {
		t.Errorf("ClaimQuery() = %+v, want an unassigned ambiguous claim", claim)
	}
}

func TestOvercastService_alias(t *testing.T) {
	// Given: a normalized Smithy identity that differs from Overcast's key.

	// When: the registry reports the service identity.
	got := overcastService("secrets-manager")

	// Then: it preserves the established router key explicitly.
	if got != "secretsmanager" {
		t.Errorf("overcastService() = %q, want secretsmanager", got)
	}
}

func TestRegistryLookup_hasNoAllocations(t *testing.T) {
	// Given: the immutable generated registry.
	registry := NewRegistry()

	// When: representative target and Query operations are repeatedly classified.
	targetAllocs := testing.AllocsPerRun(1_000, func() {
		_, _ = registry.ClaimTarget("GameLift.ListBuilds")
	})
	queryAllocs := testing.AllocsPerRun(1_000, func() {
		_, _ = registry.ClaimQuery("2016-11-15", "DescribeInstances")
	})

	// Then: lookup does not allocate model-sized data or request-local state.
	if targetAllocs != 0 || queryAllocs != 0 {
		t.Errorf("lookup allocations = target %.1f, query %.1f; want zero", targetAllocs, queryAllocs)
	}
}

func TestGeneratedIndexes_areStrictlySorted(t *testing.T) {
	// Given: the generated immutable lookup indexes.

	// When: adjacent entries are inspected in generation order.
	for i := 1; i < len(targetOperations); i++ {
		if targetOperations[i-1].Target >= targetOperations[i].Target {
			t.Fatalf("target index is not strictly sorted at %d: %q >= %q", i, targetOperations[i-1].Target, targetOperations[i].Target)
		}
	}
	for i := 1; i < len(queryOperations); i++ {
		previous, current := queryOperations[i-1], queryOperations[i]
		if previous.Version > current.Version || (previous.Version == current.Version && previous.Operation >= current.Operation) {
			t.Fatalf("Query index is not strictly sorted at %d: (%q, %q) >= (%q, %q)", i, previous.Version, previous.Operation, current.Version, current.Operation)
		}
	}
	for i := 1; i < len(serviceAliases); i++ {
		if serviceAliases[i-1].ModelService >= serviceAliases[i].ModelService {
			t.Fatalf("service alias index is not strictly sorted at %d: %q >= %q", i, serviceAliases[i-1].ModelService, serviceAliases[i].ModelService)
		}
	}

	// Then: Registry's binary searches have their required ordering invariant.
}

func BenchmarkRegistryClaimTarget(b *testing.B) {
	registry := NewRegistry()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = registry.ClaimTarget("GameLift.ListBuilds")
	}
}

func BenchmarkRegistryClaimQuery(b *testing.B) {
	registry := NewRegistry()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = registry.ClaimQuery("2016-11-15", "DescribeInstances")
	}
}
