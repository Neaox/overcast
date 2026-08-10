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
	// #854 — the whole AppConfig surface is served under /_appconfig. Worst of
	// the set: POST /applications is silently answered by AppRegistry, which
	// returns a servicecatalog ARN with 200 rather than a 501.
	//
	// These twelve are the only rows in this ledger whose modeled binding does
	// not answer a clean 501 to a correctly signed request. Every other
	// unserved binding falls through to restFallback, which asks the generated
	// registry who owns the path and answers NotImplemented — the emulator
	// being honest about a gap. AppConfig's do not, because AppRegistry
	// registers r.Route("/applications", …) (internal/services/appregistry/
	// service.go:80), and a chi sub-router owns its whole subtree: a path it
	// does not match hits *its* NotFound, never the parent's "/*". So a client
	// calling AppConfig gets a bare 404 with no AWS error body at all.
	//
	// The general rule, worth knowing before adding a prefix route: the 501
	// fallback only protects paths no other service has claimed.
	"appconfig/CreateConfigurationProfile":       "#854",
	"appconfig/CreateEnvironment":                "#854",
	"appconfig/CreateHostedConfigurationVersion": "#854",
	"appconfig/DeleteConfigurationProfile":       "#854",
	"appconfig/DeleteEnvironment":                "#854",
	"appconfig/DeleteHostedConfigurationVersion": "#854",
	"appconfig/GetConfigurationProfile":          "#854",
	"appconfig/GetEnvironment":                   "#854",
	"appconfig/GetHostedConfigurationVersion":    "#854",
	"appconfig/ListConfigurationProfiles":        "#854",
	"appconfig/ListEnvironments":                 "#854",
	"appconfig/ListHostedConfigurationVersions":  "#854",

	// #855 — served under /_appconfigdata, and the session token is a path
	// segment where the model binds it as a member.
	"appconfigdata/GetLatestConfiguration":    "#855",
	"appconfigdata/StartConfigurationSession": "#855",

	// #856 — served under /_opensearch rather than the /2021-01-01 API version
	// prefix every OpenSearch binding carries.
	"opensearch/AddTags":         "#856",
	"opensearch/CreateDomain":    "#856",
	"opensearch/DeleteDomain":    "#856",
	"opensearch/DescribeDomain":  "#856",
	"opensearch/DescribeDomains": "#856",
	"opensearch/ListDomainNames": "#856",
	"opensearch/ListTags":        "#856",
	"opensearch/RemoveTags":      "#856",

	// #857 — served under /_bedrock rather than /model/{modelId}/invoke.
	"bedrock/Converse":    "#857",
	"bedrock/InvokeModel": "#857",

	// #858 — six hand-invented paths, four with the wrong HTTP method,
	// written by hand next to a manifest that already had the answers.
	"eks/DescribeAddonConfiguration":     "#858",
	"eks/DescribeAddonVersions":          "#858",
	"eks/DescribeIdentityProviderConfig": "#858",
	"eks/ListInsights":                   "#858",
	"eks/UpdateAddon":                    "#858",
	"eks/UpdateNodegroupVersion":         "#858",

	// #859 — the v2 API is served at /v2/clusters where the model binds
	// /api/v2/clusters. The v1 surface is reachable: it is claimed by
	// /v1/clusters/* wildcards whose handlers parse the tail themselves, which
	// is why only the v2 pair is listed here.
	"msk/CreateClusterV2":   "#859",
	"msk/DescribeClusterV2": "#859",

	// #860 — the evaluation operations are API-independent in the model
	// (/v1/dataplane-evaluatecode) and are served under /v1/apis/{apiId}/….
	// The Event API channel namespaces are registered where AWS binds them and
	// are not part of this fault.
	"appsync/EvaluateCode":            "#860",
	"appsync/EvaluateMappingTemplate": "#860",

	// #862 is deliberately absent. SESv2's CreateEmailIdentity was registered
	// as PUT where AWS models POST, and #871 moved it while this branch was in
	// flight. The entry was here; this gate's second direction reported it as a
	// ledger row naming a binding that is now served, which is the ratchet
	// doing the job an exemption list cannot.

	// #815 — Backup's Inert tier answers on invented paths.
	"backup/CreateBackupPlan":    "#815",
	"backup/CreateBackupVault":   "#815",
	"backup/DeleteBackupPlan":    "#815",
	"backup/DeleteBackupVault":   "#815",
	"backup/DescribeBackupVault": "#815",
	"backup/GetBackupPlan":       "#815",
	"backup/ListBackupPlans":     "#815",
	"backup/ListBackupVaults":    "#815",
	"backup/UpdateBackupPlan":    "#815",
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

	// AppRegistry has no tag routes of its own: API Gateway's ARN-keyed store
	// is the declared fallback owner for every ARN no service claims, and has
	// answered AppRegistry's since before either had a capability table.
	"appregistry/ListTagsForResource": "another service's fallback handler",
	"appregistry/TagResource":         "another service's fallback handler",
	"appregistry/UntagResource":       "another service's fallback handler",

	// AppConfig's own tag routes are under /_appconfig/tags, so a client
	// calling the modeled /tags/{ResourceArn} reaches API Gateway's store
	// instead — the same mis-attribution #854 records for POST /applications,
	// and it goes away when #854 moves AppConfig onto its modeled paths.
	"appconfig/ListTagsForResource": "another service's fallback handler",
	"appconfig/TagResource":         "another service's fallback handler",
	"appconfig/UntagResource":       "another service's fallback handler",
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
	// It is the one entry in these ledgers with no issue behind it. It wants
	// one: unlike the routing faults, nothing here records who will encode
	// those shapes or when.
	"cloudwatch/GetMetricData": "no JSON encoding for MetricDataQueries/MetricDataResults; exempted in cloudwatch's own dispatch test since #794 — needs an issue",
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
