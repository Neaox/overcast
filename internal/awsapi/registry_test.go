package awsapi

import (
	"strings"
	"testing"
)

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

func TestRegistryClaimREST_modeledOperation(t *testing.T) {
	// Given: the immutable REST trie generated from the pinned model corpus.
	registry := NewRegistry()

	// When: a modeled Access Analyzer operation is looked up by its HTTP binding.
	claim, ok := registry.ClaimREST("GET", "/analyzer")

	// Then: it selects the REST JSON 501 envelope without S3 participation.
	if !ok {
		t.Fatal("ClaimREST() did not recognize Access Analyzer ListAnalyzers")
	}
	if claim.ModelService != "accessanalyzer" || claim.Operation != "ListAnalyzers" {
		t.Errorf("ClaimREST() = %+v, want accessanalyzer ListAnalyzers", claim)
	}
	if claim.ErrorProfile != ErrorProfileJSON {
		t.Errorf("ClaimREST() error profile = %v, want JSON", claim.ErrorProfile)
	}
}

func TestRegistryClaimREST_greedyLabelWithSuffix(t *testing.T) {
	// Given: a modeled REST URI whose greedy label is followed by a literal.
	registry := NewRegistry()

	// When: its label spans multiple path segments.
	claim, ok := registry.ClaimREST("GET", "/v20180820/mrap/instances/one/two/policy")

	// Then: matching reaches the modeled operation rather than S3.
	if !ok || claim.Operation != "GetMultiRegionAccessPointPolicy" {
		t.Errorf("ClaimREST() = %+v, %v; want GetMultiRegionAccessPointPolicy", claim, ok)
	}
}

func TestRegistryClaimREST_rootBinding(t *testing.T) {
	// Given: a modeled REST operation bound to the HTTP root.
	registry := NewRegistry()

	// When: MediaStore Data's root GET binding is classified.
	claim, ok := registry.ClaimREST("GET", "/")

	// Then: the registry retains the signing name needed to distinguish it from S3.
	if !ok || claim.Operation != "ListItems" || claim.SigningName != "mediastore" {
		t.Errorf("ClaimREST() = %+v, %v; want MediaStore Data ListItems", claim, ok)
	}
}

func TestRegistryClaimREST_literalQueryBinding(t *testing.T) {
	// Given: two modeled operations share a path and method but use distinct
	// literal query bindings.
	registry := NewRegistry()

	// When: the TagResource query discriminator is present.
	claim, ok := registry.ClaimRESTQuery("POST", "/tags", "operation=tag-resource")

	// Then: the query-aware trie selects that binding instead of collapsing it
	// into another operation on POST /tags.
	if !ok || claim.Operation != "TagResource" {
		t.Errorf("ClaimRESTQuery() = %+v, %v; want TagResource", claim, ok)
	}
}

func TestRegistryClaimRPC_additiveCBORProtocol(t *testing.T) {
	// Given: GameLift's canonical AWS JSON service model also advertises
	// Smithy RPC v2 CBOR as an additive protocol.
	registry := NewRegistry()

	// When: its Smithy RPC route is classified by service shape and operation.
	claim, ok := registry.ClaimRPC(ProtocolRPCV2CBOR, "GameLift", "ListBuilds")

	// Then: the generated RPC index owns the route independently of the
	// service's canonical protocol.
	if !ok || claim.ModelService != "gamelift" || claim.Operation != "ListBuilds" {
		t.Errorf("ClaimRPC() = %+v, %v; want gamelift ListBuilds", claim, ok)
	}
	if claim.ErrorProfile != ErrorProfileRPCV2CBOR {
		t.Errorf("ClaimRPC() error profile = %v, want RPC v2 CBOR", claim.ErrorProfile)
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

func TestGeneratedRESTCollisions_areReported(t *testing.T) {
	// Given: the generated REST collision index.

	// When: its entries are inspected.
	if len(restCollisions) == 0 {
		t.Fatal("generated REST collision index is empty")
	}
	for _, collision := range restCollisions {
		// Then: every collision retains a readable key and all competing services.
		if collision.Key == "" || len(collision.Services) < 2 {
			t.Errorf("invalid REST collision: %+v", collision)
		}
	}
}

func TestGeneratedRPCCollisions_areWellFormed(t *testing.T) {
	for _, collision := range rpcCollisions {
		if collision.Key == "" || len(collision.Services) < 2 {
			t.Errorf("invalid RPC collision: %+v", collision)
		}
	}
}

func TestGeneratedCorpus_everyNonS3OperationHasRouteOwnership(t *testing.T) {
	// Given: every operation in the pinned Smithy corpus and the immutable
	// indexes generated from that same input.
	registry := NewRegistry()
	var uncovered []string

	// When: each operation is reduced to its safe wire discriminator.
	for _, op := range manifest {
		if op.Service == "s3" {
			continue
		}
		owned := false
		switch op.Protocol {
		case ProtocolUnknown:
			// Unknown protocols deliberately remain uncovered so the assertion
			// below detects them.
		case ProtocolAWSJSON10, ProtocolAWSJSON11:
			if op.TargetPrefix != "" {
				_, owned = registry.ClaimTarget(op.TargetPrefix + op.Name)
			}
		case ProtocolAWSQuery, ProtocolEC2Query:
			_, owned = registry.ClaimQuery(op.APIVersion, op.Name)
		case ProtocolRESTJSON, ProtocolRESTXML:
			path, query := corpusRESTRequest(op.URI)
			_, owned = registry.ClaimRESTQuery(op.HTTPMethod, path, query)
		case ProtocolRPCV2CBOR, ProtocolRPCV2JSON:
			// RPC traits, including additive traits, are checked uniformly
			// below rather than only when RPC is canonical.
		}
		if op.Protocols&ProtocolsRPCV2CBOR != 0 {
			_, rpcOwned := registry.ClaimRPC(ProtocolRPCV2CBOR, op.ServiceShape, op.Name)
			owned = owned || rpcOwned
		}
		if op.Protocols&ProtocolsRPCV2JSON != 0 {
			_, rpcOwned := registry.ClaimRPC(ProtocolRPCV2JSON, op.ServiceShape, op.Name)
			owned = owned || rpcOwned
		}
		if !owned && len(uncovered) < 50 {
			uncovered = append(uncovered, op.Service+"/"+op.Name+" ("+string(op.Protocol)+")")
		}
	}

	// Then: no modeled non-S3 operation can rely on S3 as its owner.
	if len(uncovered) > 0 {
		t.Fatalf("generated corpus contains operations without route ownership (first %d):\n%s", len(uncovered), strings.Join(uncovered, "\n"))
	}
}

func corpusRESTRequest(uri string) (path, query string) {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		path, query = uri[:i], uri[i+1:]
	} else {
		path = uri
	}
	var out strings.Builder
	out.Grow(len(path))
	for start := 0; start < len(path); {
		open := strings.IndexByte(path[start:], '{')
		if open < 0 {
			out.WriteString(path[start:])
			break
		}
		open += start
		out.WriteString(path[start:open])
		close := strings.IndexByte(path[open:], '}')
		if close < 0 {
			out.WriteString(path[open:])
			break
		}
		close += open
		if path[close-1] == '+' {
			out.WriteString("value/part")
		} else {
			out.WriteString("value")
		}
		start = close + 1
	}
	return out.String(), query
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

func TestHasOperation_usesOvercastServiceAlias(t *testing.T) {
	// Given: a capability uses Overcast's established service key.

	// When: capgen validates it against the modeled operation corpus.
	got := HasOperation("secretsmanager", "ListSecrets")

	// Then: the model's secrets-manager identity resolves through the alias.
	if !got {
		t.Fatal("HasOperation() did not resolve secretsmanager/ListSecrets")
	}
}

func TestHasOperation_resolvesLegacyServiceFamilies(t *testing.T) {
	// Given: Overcast service keys that cover a separately modeled AWS family.
	tests := []struct {
		service   string
		operation string
	}{
		{service: "bedrock", operation: "InvokeModel"},
		{service: "cognito", operation: "ListUsers"},
	}

	// When: build tooling validates their capabilities against the corpus.
	for _, test := range tests {
		if !HasOperation(test.service, test.operation) {
			t.Errorf("HasOperation(%q, %q) = false, want true", test.service, test.operation)
		}
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
	restAllocs := testing.AllocsPerRun(1_000, func() {
		_, _ = registry.ClaimREST("GET", "/analyzer")
	})
	rpcAllocs := testing.AllocsPerRun(1_000, func() {
		_, _ = registry.ClaimRPC(ProtocolRPCV2CBOR, "GameLift", "ListBuilds")
	})

	// Then: lookup does not allocate model-sized data or request-local state.
	if targetAllocs != 0 || queryAllocs != 0 || restAllocs != 0 || rpcAllocs != 0 {
		t.Errorf("lookup allocations = target %.1f, query %.1f, REST %.1f, RPC %.1f; want zero", targetAllocs, queryAllocs, restAllocs, rpcAllocs)
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
	for i := 1; i < len(rpcOperations); i++ {
		previous, current := rpcOperations[i-1], rpcOperations[i]
		if compareRPCOperation(previous, current) >= 0 {
			t.Fatalf("RPC index is not strictly sorted at %d: %+v >= %+v", i, previous, current)
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

func BenchmarkRegistryClaimREST(b *testing.B) {
	registry := NewRegistry()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = registry.ClaimREST("GET", "/analyzer")
	}
}

func BenchmarkRegistryClaimRPC(b *testing.B) {
	registry := NewRegistry()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = registry.ClaimRPC(ProtocolRPCV2CBOR, "GameLift", "ListBuilds")
	}
}
