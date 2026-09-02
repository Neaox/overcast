package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/state"
)

// preflightRegionRequest issues a request at the handler with region as the
// selected region, the way middleware.Region would have put it in context.
func preflightRegionRequest(t *testing.T, store state.Store, defaultRegion, kind, region string) *httptest.ResponseRecorder {
	t.Helper()
	handler := newPreflightRegionHandler(&config.Config{Region: defaultRegion}, store)
	req := httptest.NewRequest(http.MethodGet, "/_overcast/preflight/region?kind="+kind, nil)
	if region != "" {
		req = req.WithContext(middleware.ContextWithRegion(req.Context(), region))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodePreflightRegion(t *testing.T, rec *httptest.ResponseRecorder) preflightRegionResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got preflightRegionResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return got
}

// seedStacks writes one CloudFormation stack record per name under region.
func seedStacks(t *testing.T, store state.Store, region string, statusByName map[string]string) {
	t.Helper()
	for name, status := range statusByName {
		raw, err := json.Marshal(map[string]any{"StackName": name, "StackStatus": status})
		if err != nil {
			t.Fatalf("marshal stack %s: %v", name, err)
		}
		if err := store.Set(context.Background(), "cfn:stacks", region+"/"+name, string(raw)); err != nil {
			t.Fatalf("seed stack %s: %v", name, err)
		}
	}
}

// The case the whole check exists for: the console is looking at us-east-1
// because that is what it defaults to, the developer's tooling deployed into
// ap-southeast-2 because that is what AWS_REGION says, and neither side has
// any reason to mention the other.
func TestPreflightRegion_namesTheRegionThatHasTheResources(t *testing.T) {
	store := state.NewMemoryStore()
	seedStacks(t, store, "ap-southeast-2", map[string]string{
		"orders-api":  "CREATE_COMPLETE",
		"payments":    "UPDATE_COMPLETE",
		"edge-assets": "CREATE_COMPLETE",
	})

	got := decodePreflightRegion(t, preflightRegionRequest(t, store, "us-east-1", "cloudformation-stacks", "us-east-1"))

	if got.Region != "us-east-1" || got.Count != 0 {
		t.Fatalf("expected an empty us-east-1, got %+v", got)
	}
	if len(got.Elsewhere) != 1 {
		t.Fatalf("expected exactly one other region, got %+v", got.Elsewhere)
	}
	if got.Elsewhere[0].Region != "ap-southeast-2" || got.Elsewhere[0].Count != 3 {
		t.Fatalf("expected ap-southeast-2 with 3 stacks, got %+v", got.Elsewhere[0])
	}
}

// The anti-cry-wolf case, and the most important test here. An account with
// nothing in it anywhere is the overwhelmingly common way a list page comes
// back empty — a fresh emulator, or one whose data a restart discarded. A
// check that says anything at all here is a check people learn to skip past,
// and it takes every other preflight check down with it.
func TestPreflightRegion_saysNothingWhenThereIsNothingAnywhere(t *testing.T) {
	got := decodePreflightRegion(t, preflightRegionRequest(t, state.NewMemoryStore(), "us-east-1", "cloudformation-stacks", "us-east-1"))

	if got.Count != 0 {
		t.Fatalf("expected an empty selected region, got %+v", got)
	}
	if len(got.Elsewhere) != 0 {
		t.Fatalf("expected no advisory at all, got %+v", got.Elsewhere)
	}
}

// A region that holds only deleted stacks holds nothing a console list page
// would have shown, so pointing at it would send the developer somewhere just
// as empty as where they started. This is the one kind whose records outlive
// the resource, which is why the kind carries a predicate at all.
func TestPreflightRegion_ignoresStacksThatAreAlreadyDeleted(t *testing.T) {
	store := state.NewMemoryStore()
	seedStacks(t, store, "ap-southeast-2", map[string]string{
		"orders-api": "DELETE_COMPLETE",
		"payments":   "DELETE_COMPLETE",
	})

	got := decodePreflightRegion(t, preflightRegionRequest(t, store, "us-east-1", "cloudformation-stacks", "us-east-1"))

	if len(got.Elsewhere) != 0 {
		t.Fatalf("expected no advisory for a region holding only deleted stacks, got %+v", got.Elsewhere)
	}
}

// The symptom is an empty list. A region with resources in it has no symptom
// to explain, so the answer is silence — and specifically not a running tally
// of what the neighbours hold, which would turn a diagnosis into a dashboard.
func TestPreflightRegion_saysNothingWhenTheSelectedRegionHasResources(t *testing.T) {
	store := state.NewMemoryStore()
	seedStacks(t, store, "us-east-1", map[string]string{"orders-api": "CREATE_COMPLETE"})
	seedStacks(t, store, "ap-southeast-2", map[string]string{"payments": "CREATE_COMPLETE"})

	got := decodePreflightRegion(t, preflightRegionRequest(t, store, "us-east-1", "cloudformation-stacks", "us-east-1"))

	if got.Count != 1 {
		t.Fatalf("expected the selected region's own count, got %+v", got)
	}
	if len(got.Elsewhere) != 0 {
		t.Fatalf("expected no advisory when the page is not empty, got %+v", got.Elsewhere)
	}
}

// Several regions is the case where ordering earns its keep: the region most
// likely to be the one they meant is the one holding the most.
func TestPreflightRegion_ordersOtherRegionsByHowMuchTheyHold(t *testing.T) {
	store := state.NewMemoryStore()
	seedStacks(t, store, "eu-west-1", map[string]string{"a": "CREATE_COMPLETE"})
	seedStacks(t, store, "ap-southeast-2", map[string]string{"b": "CREATE_COMPLETE", "c": "CREATE_COMPLETE"})

	got := decodePreflightRegion(t, preflightRegionRequest(t, store, "us-east-1", "cloudformation-stacks", "us-east-1"))

	if len(got.Elsewhere) != 2 {
		t.Fatalf("expected both regions, got %+v", got.Elsewhere)
	}
	if got.Elsewhere[0].Region != "ap-southeast-2" || got.Elsewhere[1].Region != "eu-west-1" {
		t.Fatalf("expected the fuller region first, got %+v", got.Elsewhere)
	}
}

// Records written before region-scoped keys existed carry no region prefix.
// serviceutil.ScanRegions attributes those to the configured default, and this
// has to agree with it: attributing them to a region called "" would invent a
// region nobody can select and advise the developer to go there.
func TestPreflightRegion_attributesUnprefixedKeysToTheDefaultRegion(t *testing.T) {
	store := state.NewMemoryStore()
	raw, err := json.Marshal(map[string]any{"StackName": "legacy", "StackStatus": "CREATE_COMPLETE"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Set(context.Background(), "cfn:stacks", "legacy", string(raw)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := decodePreflightRegion(t, preflightRegionRequest(t, store, "us-east-1", "cloudformation-stacks", "eu-west-1"))

	if len(got.Elsewhere) != 1 || got.Elsewhere[0].Region != "us-east-1" {
		t.Fatalf("expected the unprefixed record to count as us-east-1, got %+v", got.Elsewhere)
	}
}

// livingRecordPerKind is one stored record per registered kind, in the shape
// that kind's service actually persists — a record its predicate must accept
// and its namespace must hold.
//
// It is keyed by kind rather than derived from the registry so that adding a
// kind is not silently self-certifying: the new entry has to be written out
// here by someone who went and looked at what the service stores.
var livingRecordPerKind = map[string]string{
	"cloudformation-stacks": `{"StackName":"orders-api","StackStatus":"CREATE_COMPLETE"}`,
	"sqs-queues":            `{"name":"orders","arn":"arn:aws:sqs:ap-southeast-2:000000000000:orders"}`,
	"lambda-functions":      `{"name":"handler","arn":"arn:aws:lambda:ap-southeast-2:000000000000:function:handler"}`,
	"dynamodb-tables":       `{"TableName":"orders","TableStatus":"ACTIVE","TableArn":"arn:aws:dynamodb:ap-southeast-2:000000000000:table/orders"}`,
	"sns-topics":            `{"name":"orders","arn":"arn:aws:sns:ap-southeast-2:000000000000:orders","created_timestamp":1700000000}`,
	"kinesis-streams":       `{"StreamName":"orders","StreamARN":"arn:aws:kinesis:ap-southeast-2:000000000000:stream/orders","StreamStatus":"ACTIVE"}`,
}

// Every kind in the registry has to be reachable by the name the console asks
// for, and none may name a namespace that no longer exists. A kind that reads
// an empty namespace is indistinguishable from a healthy account, which is the
// failure this check is least able to notice about itself.
func TestPreflightRegion_everyKindCountsTheRecordsItsNamespaceHolds(t *testing.T) {
	for _, kind := range preflightRegionKinds {
		t.Run(kind.kind, func(t *testing.T) {
			record, ok := livingRecordPerKind[kind.kind]
			if !ok {
				t.Fatalf("kind %q has no sample record in livingRecordPerKind — add one from what %s actually stores", kind.kind, kind.namespace)
			}
			store := state.NewMemoryStore()
			if err := store.Set(context.Background(), kind.namespace, "ap-southeast-2/thing", record); err != nil {
				t.Fatalf("seed %s: %v", kind.namespace, err)
			}

			got := decodePreflightRegion(t, preflightRegionRequest(t, store, "us-east-1", kind.kind, "us-east-1"))

			if len(got.Elsewhere) != 1 || got.Elsewhere[0].Count != 1 {
				t.Fatalf("kind %q did not count its own record: %+v", kind.kind, got.Elsewhere)
			}
		})
	}
}

// An unknown kind is a console bug, not a diagnosis — answering 200 with an
// empty advisory would hide it forever behind a check that simply never fires.
func TestPreflightRegion_rejectsAKindItDoesNotKnow(t *testing.T) {
	rec := preflightRegionRequest(t, state.NewMemoryStore(), "us-east-1", "not-a-kind", "us-east-1")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown kind, got %d: %s", rec.Code, rec.Body.String())
	}
}

// With no region on the request the handler must fall back to the same default
// every region-scoped store read falls back to, or it would compare the
// console's region against a partition nothing is stored under.
func TestPreflightRegion_fallsBackToTheConfiguredDefaultRegion(t *testing.T) {
	store := state.NewMemoryStore()
	seedStacks(t, store, "ap-southeast-2", map[string]string{"orders-api": "CREATE_COMPLETE"})

	got := decodePreflightRegion(t, preflightRegionRequest(t, store, "eu-west-2", "cloudformation-stacks", ""))

	if got.Region != "eu-west-2" {
		t.Fatalf("expected the configured default region, got %q", got.Region)
	}
	if len(got.Elsewhere) != 1 {
		t.Fatalf("expected the advisory to still fire, got %+v", got.Elsewhere)
	}
}
