package awsapi

import (
	"reflect"
	"testing"
)

// scanForOperation is the definition KnownOperation's index has to agree with:
// a linear walk of the whole corpus, resolving each modeled service to its
// Overcast key. It lives in the test rather than in the package because the
// index is the only implementation a caller should reach — but the index is
// only trustworthy if it answers exactly what the scan would.
func scanForOperation(service, name string) bool {
	for _, op := range manifest {
		if overcastService(op.Service) == service && op.Name == name {
			return true
		}
	}
	return false
}

// TestKnownOperation_matchesACorpusScan walks every modeled operation and
// asserts the index agrees with the scan on it, then asserts it agrees on
// names and keys the corpus does not carry. The positive half covers every
// (key, operation) pair in the corpus, so an alias the index built wrongly —
// "logs" left unresolved to "cloudwatch-logs", say — cannot pass.
func TestKnownOperation_matchesACorpusScan(t *testing.T) {
	// Given: every pair the corpus carries.
	seen := 0
	WalkOperations(func(op Operation) bool {
		seen++
		key := ServiceKey(op.Service)

		// When/Then: the index answers what a scan would.
		if !KnownOperation(key, op.Name) {
			t.Fatalf("KnownOperation(%q, %q) = false, want true (modeled as %s)", key, op.Name, op.Service)
		}
		return true
	})
	if seen == 0 {
		t.Fatal("the model corpus is empty; the index would be vacuously correct")
	}

	// Given: pairs the corpus does not carry, including a real operation
	// asked for under the modeled identity rather than the Overcast key.
	misses := []struct {
		service, operation, reason string
	}{
		{"dynamodb", "NotAnOperation", "invented operation name"},
		{"logs", "CreateLogGroup", "\"logs\" is a modeled identity, not the Overcast key"},
		{"streams.dynamodb", "ListStreams", "\"streams.dynamodb\" is a modeled identity, not the Overcast key"},
		{"notaservice", "ListTables", "invented service key"},
		{"", "", "empty pair"},
		{"cloudwatch", "CreateLogGroup", "real operation, wrong service"},
	}
	for _, m := range misses {
		if want := scanForOperation(m.service, m.operation); want {
			t.Fatalf("test setup: the corpus carries (%q, %q); pick a pair it does not", m.service, m.operation)
		}
		if KnownOperation(m.service, m.operation) {
			t.Errorf("KnownOperation(%q, %q) = true, want false (%s)", m.service, m.operation, m.reason)
		}
	}
}

// TestKnownOperation_resolvesAliasedServiceKeys pins the two aliases that make
// an index necessary in the first place: manifest is sorted by the modeled
// service, and these keys sort somewhere else once resolved, so no binary
// search over manifest could answer for them.
func TestKnownOperation_resolvesAliasedServiceKeys(t *testing.T) {
	// Given: Overcast keys whose models are filed under another name.
	tests := []struct {
		service, operation string
	}{
		{"cloudwatch-logs", "CreateLogGroup"},
		{"dynamodbstreams", "ListStreams"},
		{"secretsmanager", "ListSecrets"},
		{"stepfunctions", "CreateStateMachine"},
	}

	for _, test := range tests {
		// When: a refusal path asks whether AWS models the operation.
		got := KnownOperation(test.service, test.operation)

		// Then: the alias resolves, as it does for an unaliased key.
		if !got {
			t.Errorf("KnownOperation(%q, %q) = false, want true", test.service, test.operation)
		}
		if !IsServiceKey(test.service) {
			t.Errorf("IsServiceKey(%q) = false; the refusal path's key must be a service key", test.service)
		}
	}
}

// warmIndex builds the index before a timed loop, so what is reported is the
// steady-state lookup rather than one amortised share of the single build.
// The build is a one-off ~2ms over the whole corpus, paid on the first
// refusal a process serves and never again.
func warmIndex() { _ = KnownOperation("", "") }

// BenchmarkKnownOperationHit and its Miss twin are the reason the index
// exists: WriteUnhandledOperation runs this per refused request, on a name an
// arbitrary caller chooses, so the miss is the case that matters. Compare
// them with BenchmarkCorpusScanMiss, which is the linear walk they replaced —
// the index answers in constant time where the scan pays the corpus' full
// length (~19k entries, each resolved through overcastService).
func BenchmarkKnownOperationHit(b *testing.B) {
	warmIndex()
	b.ReportAllocs()
	for b.Loop() {
		_ = KnownOperation("dynamodb", "CreateBackup")
	}
}

func BenchmarkKnownOperationMiss(b *testing.B) {
	warmIndex()
	b.ReportAllocs()
	for b.Loop() {
		_ = KnownOperation("dynamodb", "NotAnOperation")
	}
}

func BenchmarkCorpusScanMiss(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = scanForOperation("dynamodb", "NotAnOperation")
	}
}

// TestKnownOperations_isBuiltOnceAndCoversTheCorpus pins the two properties
// the benchmarks' constant-time claim rests on, without timing anything:
// every lookup reads the same map, and that map holds every distinct
// (Overcast key, operation) pair the corpus carries. A per-call rebuild, or a
// build that dropped pairs, would fail here rather than only showing up as a
// slow refusal on a live request.
func TestKnownOperations_isBuiltOnceAndCoversTheCorpus(t *testing.T) {
	// Given: the index, read twice.
	first, second := knownOperations(), knownOperations()
	if len(first) == 0 {
		t.Fatal("knownOperations() is empty")
	}

	// Then: both reads are the same map — a map header points at one bucket
	// array, so equal pointers mean sync.OnceValue built it once.
	if reflect.ValueOf(first).Pointer() != reflect.ValueOf(second).Pointer() {
		t.Error("knownOperations() returned a different map on the second call; the index is being rebuilt per lookup")
	}

	// And: it holds exactly the corpus' distinct pairs.
	want := make(map[operationKey]struct{}, len(manifest))
	for _, op := range manifest {
		want[operationKey{service: overcastService(op.Service), name: op.Name}] = struct{}{}
	}
	if len(first) != len(want) {
		t.Errorf("index holds %d pairs, the corpus has %d distinct ones", len(first), len(want))
	}
}
