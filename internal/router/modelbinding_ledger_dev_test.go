//go:build dev

package router

import (
	"sort"
	"strings"
	"testing"
)

// unservedBindings is the fallout ledger: every capability Overcast declares
// it dispatches whose modeled binding no route serves, and the issue that owns
// the fix.
//
// It is a ratchet, not an exemption list, and the distinction is the whole
// point of #864. An exemption says "do not ask about this row"; this ledger
// says "this row is known-broken, here is who owns it, and the build fails the
// moment that stops being true". Concretely, TestModeledBindings_… fails when
//
//   - a binding is unserved and absent from this map — a new fault, or an old
//     one uncovered by a model refresh; or
//   - a binding listed here is now served — the fix landed and the entry has
//     to go, so the ledger cannot quietly outlive the fault it records.
//
// Every entry names a filed issue. Adding one without an issue is how the
// situation #864 describes came about: capgen's exemption table stayed small
// and honest, and the checks that were never written stayed unwritten.
//
// Each of these services carries the same standing requirement before its
// routes are moved (stated on #854-#860 and #862): review the implementation
// in full against CONTRIBUTING.md, and establish AWS fidelity from the pinned
// model rather than by assumption. That is why this change lands the gate and
// the ledger rather than 66 route moves — the fixes are per-service work with
// per-service review, and #793 is the precedent for what a rushed one costs.
var unservedBindings = map[string]string{
	// #854 is deliberately absent, and it is the entry worth reading the
	// history of. AppConfig's twelve rows were the only ones in this ledger
	// whose modeled binding did not answer a clean 501: AppRegistry registered
	// r.Route("/applications", …), and a chi sub-router owns its whole subtree,
	// so a path it did not match hit *its* NotFound rather than the parent's
	// "/*". A client calling AppConfig got a bare 404 with no AWS error body —
	// or, on the four paths AppRegistry did match, a Service Catalog resource
	// with a 200.
	//
	// The main router now owns /applications and picks the service from the
	// SigV4 credential scope, and both sub-routers hand a path they do not
	// serve back to restFallback. The general rule the fault taught, worth
	// knowing before adding a prefix route: the 501 fallback only protects
	// paths no other service has claimed.

	// #855 is deliberately absent. AppConfig Data was served under
	// /_appconfigdata with the session token as a path segment; both operations
	// now answer at POST /configurationsessions and GET /configuration, with
	// the token read from the configuration_token query member the model binds
	// it to.

	// #856 is deliberately absent. OpenSearch's eight operations were served
	// under /_opensearch; they now answer on the /2021-01-01 bindings the
	// model gives them, so the ledger must not still claim the fault.

	// #860 is deliberately absent. AppSync's two evaluation operations were
	// served under /v1/apis/{apiId}/… where the model binds them to the
	// API-independent /v1/dataplane-evaluatecode and /v1/dataplane-evaluatetemplate;
	// they are now registered there.

	// #862 is deliberately absent. SESv2's CreateEmailIdentity was registered
	// as PUT where AWS models POST, and #871 moved it while this branch was in
	// flight. The entry was here; this gate's second direction reported it as a
	// ledger row naming a binding that is now served, which is the ratchet
	// doing the job an exemption list cannot.

	// #815 is deliberately absent too, and it was the largest entry here: all
	// nine Backup operations, because the service registered no chi routes at
	// all and dispatched off an invented `AWSBackup.` target prefix. They are
	// now served at their modeled bindings under /backup-vaults and
	// /backup/plans.
}

// weaklyServedBindings records the operations the router delivers without
// proving the declaring service serves them. See
// TestWeaklyServedBindings_areRecorded for what the two grades mean.
//
// This is not a fault list — every entry here works today. It is the honest
// margin of the binding gate, kept as data so it cannot grow unnoticed. A
// service that adopts a wildcard where it previously registered routes gives
// up build-time proof of its whole surface, and that trade should be a diff
// somebody reads rather than a number nobody sees.
var weaklyServedBindings = map[string]string{
	// MSK routes the whole v1 cluster subtree to three dispatch handlers that
	// pick the operation out of the path themselves. The operations addressed
	// by ARN alone are still proven exactly; only the two with a sub-resource
	// beneath the ARN rest on the handler's own routing.
	"msk/GetBootstrapBrokers":        "a route broader than the modeled binding",
	"msk/UpdateClusterConfiguration": "a route broader than the modeled binding",

	// AppRegistry's three tag rows were here as "another service's fallback
	// handler" until #976: API Gateway's ARN-keyed store answered every ARN no
	// service claimed, AppRegistry's among them. The /tags dispatcher now
	// routes "servicecatalog" ARNs to that store by name — the store is the
	// same, so the rows still work, and the mount recorded for it proves them
	// exactly. AppConfig's three were here too until #854 gave it a TagsRouter
	// of its own.
}

// protocolAsymmetries records operations reachable over one of their service's
// protocols and not another. See
// TestDeclaredProtocols_areDispatchedSymmetrically.
//
// Like unservedBindings this is a ratchet: an unrecorded asymmetry fails the
// build, and so does a recorded one that has been resolved.
var protocolAsymmetries = map[string]string{
	// Pre-existing and deliberate. CloudWatch's own
	// TestDispatchJSON_CoversEveryQueryOperation has carried the same
	// exemption since #794, with the reason "no JSON request/response shape
	// yet: MetricDataQueries, epoch timestamps and MetricDataResults all need
	// their own encoding". The model makes awsJson1_0 GetMetricData's
	// *primary* protocol, so this is the one CloudWatch operation an SDK
	// reaches only by falling back to Query.
	//
	// #886 owns the encoding work: MetricDataQueries, epoch timestamps and
	// MetricDataResults each need their own JSON shape, so it is not a rename
	// away from the Query encoder.
	"cloudwatch/GetMetricData": "#886",
}

// assertProtocolAsymmetry compares the observed asymmetries with the recorded
// ones in both directions.
func assertProtocolAsymmetry(t *testing.T, observed []string) {
	t.Helper()

	unrecorded := make([]string, 0, len(observed))
	resolved := map[string]bool{}
	for key := range protocolAsymmetries {
		resolved[key] = true
	}
	for _, finding := range observed {
		key := strings.SplitN(finding, ":", 2)[0]
		if _, recorded := protocolAsymmetries[key]; recorded {
			delete(resolved, key)
			continue
		}
		unrecorded = append(unrecorded, finding)
	}

	stale := make([]string, 0, len(resolved))
	for key := range resolved {
		stale = append(stale, key+" ("+protocolAsymmetries[key]+")")
	}
	sort.Strings(stale)

	if len(unrecorded) > 0 {
		t.Errorf("%d operation(s) are reachable over one modeled protocol and not another:\n\t%s\n\n"+
			"Dispatch the operation on both. This is the #794 fault class: the same call succeeds or 501s "+
			"depending only on which protocol the SDK happened to choose.",
			len(unrecorded), strings.Join(unrecorded, "\n\t"))
	}
	if len(stale) > 0 {
		t.Errorf("%d recorded asymmetr(ies) no longer hold — delete them from protocolAsymmetries:\n\t%s",
			len(stale), strings.Join(stale, "\n\t"))
	}
}

// assertWeaklyServed compares the observed weak-coverage set with the recorded
// one, in both directions and including the grade, so a binding that slips
// from an exact route to a wildcard is reported rather than absorbed.
func assertWeaklyServed(t *testing.T, observed map[string]string) {
	t.Helper()

	var changed []string
	for key, grade := range observed {
		switch recorded, ok := weaklyServedBindings[key]; {
		case !ok:
			changed = append(changed, key+": now served only by "+grade+", and is not recorded")
		case recorded != grade:
			changed = append(changed, key+": served by "+grade+", recorded as "+recorded)
		}
	}
	for key, grade := range weaklyServedBindings {
		if _, ok := observed[key]; !ok {
			changed = append(changed, key+": recorded as served by "+grade+", but is no longer — delete the entry")
		}
	}
	sort.Strings(changed)

	if len(changed) > 0 {
		t.Errorf("weakly served bindings changed:\n\t%s\n\n"+
			"Update weaklyServedBindings. Prefer registering the route at the modeled binding: an entry here is a "+
			"binding the build can no longer prove.", strings.Join(changed, "\n\t"))
	}
}

// assertLedger holds the observed fallout to the recorded fallout in both
// directions.
func assertLedger(t *testing.T, faults []bindingFault) {
	t.Helper()

	var unrecorded []string
	served := map[string]bool{}
	for key := range unservedBindings {
		served[key] = true
	}
	for _, fault := range faults {
		if _, recorded := unservedBindings[fault.key()]; recorded {
			delete(served, fault.key())
			continue
		}
		unrecorded = append(unrecorded, fault.String())
	}
	sort.Strings(unrecorded)

	stale := make([]string, 0, len(served))
	for key := range served {
		stale = append(stale, key+" ("+unservedBindings[key]+")")
	}
	sort.Strings(stale)

	if len(unrecorded) > 0 {
		t.Errorf("%d operation(s) are declared as dispatched but no route serves the binding AWS models:\n\t%s\n\n"+
			"Register the route at the modeled method and URI. If the fault is pre-existing and owned by a filed issue, "+
			"add it to unservedBindings with that issue — never widen an exemption to make this pass.",
			len(unrecorded), strings.Join(unrecorded, "\n\t"))
	}
	if len(stale) > 0 {
		t.Errorf("%d ledger entr(ies) name a binding that is now served — delete them from unservedBindings:\n\t%s",
			len(stale), strings.Join(stale, "\n\t"))
	}
}
