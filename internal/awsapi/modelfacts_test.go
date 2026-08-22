package awsapi

import (
	"sort"
	"strings"
	"testing"
)

// This file holds awsapi's own hand-written tables to the manifest sitting
// beside them — #864's sixth enforcement point, applied to the two tables in
// this package that restate modeled facts.
//
// Neither table can be generated. versions.go has to name an *owner* per API
// version, which the models cannot supply (DocumentDB, Neptune and RDS share
// 2014-10-31 and most of its action names, and of the three Overcast implements
// only RDS); registry_data.go has to name the Overcast key a modeled identity
// answers to, which is Overcast's decision and not AWS's. But every table like
// that has a half that *is* modeled fact, and that half is what a build can
// check. A version string, and the existence of the identity an alias names,
// are both in the manifest.
//
// The fault class is the one #864 names: a hand-typed wire fact that disagrees
// with the model in the same repository. It costs more here than in a service
// package, because both tables feed classification — a version nothing models
// leaves `Action=DescribeDBInstances` unclaimed and answered by S3's wildcard,
// and an alias whose modeled identity does not exist silently detaches every
// capability row of that service from the model gate.

// TestQueryAPIVersions_matchTheModel asserts every version constant in
// versions.go is the API version the pinned models give the service that
// versions.go says owns it.
//
// The constants exist because the Query protocol identifies a service by
// (Version, Action) and nothing else, so a mistyped digit does not fail — it
// silently stops matching. The request then falls through the Query dispatchers
// to S3's wildcard, which is the shape of the misclassification described in
// queryVersionOwners' own comment, and of #854's worst symptom generally: a
// plausible answer from a service nobody asked.
func TestQueryAPIVersions_matchTheModel(t *testing.T) {
	// Given: the versions the models declare for Query-protocol operations,
	// indexed by the Overcast key that answers for them.
	modeled := map[string]map[string]bool{}
	for _, op := range manifest {
		if op.APIVersion == "" || op.Protocols&(ProtocolsAWSQuery|ProtocolsEC2Query) == 0 {
			continue
		}
		key := ServiceKey(op.Service)
		if modeled[key] == nil {
			modeled[key] = map[string]bool{}
		}
		modeled[key][op.APIVersion] = true
	}

	// When: each declared owner is looked up.
	// Then: the model gives that service exactly that version.
	if len(queryVersionOwners) == 0 {
		t.Fatal("queryVersionOwners is empty — this test would pass while proving nothing")
	}
	for version, service := range queryVersionOwners {
		if len(modeled[service]) == 0 {
			t.Errorf("queryVersionOwners[%q] = %q, but the models declare no Query-protocol operation for that service key",
				version, service)
			continue
		}
		if !modeled[service][version] {
			t.Errorf("queryVersionOwners[%q] = %q, but AWS models that service at %v — a Query request at %s matches nothing and falls into S3's wildcard",
				version, service, sortedSet(modeled[service]), version)
		}
	}
}

// TestQueryVersionConstants_areAllOwned asserts the constants and the owner map
// describe the same set.
//
// A constant with no owner is a version the registry cannot resolve a collision
// for, which is the state queryVersionOwners was written to end; an owner for a
// version no constant names is a string nothing else in the repo agrees with.
func TestQueryVersionConstants_areAllOwned(t *testing.T) {
	constants := []string{
		VersionCloudFormation,
		VersionCloudWatch,
		VersionEC2,
		VersionRDS,
		VersionElastiCache,
		VersionELBv2,
		VersionAutoScaling,
	}
	for _, version := range constants {
		if _, ok := queryVersionOwners[version]; !ok {
			t.Errorf("version %q has no entry in queryVersionOwners — the registry cannot attribute a colliding action at that version", version)
		}
	}
	if len(queryVersionOwners) != len(constants) {
		t.Errorf("queryVersionOwners has %d entries for %d version constants; every owner must name a constant so the two cannot drift",
			len(queryVersionOwners), len(constants))
	}
}

// TestServiceAliases_areUniqueAndWellFormed covers the halves of the alias
// contract that TestServiceAliases_referenceModeledIdentities does not.
//
// That test already asserts the left-hand side is a modeled identity, which is
// the half a model refresh breaks. These are the halves a *hand edit* breaks: a
// duplicated source, which overcastService's binary search resolves to whichever
// copy it lands on, and a target that is not a service key at all.
//
// It matters because the alias is read by ServiceKey on every classification.
// registry_data.go's own comments record the cost of getting one wrong: "waf" is
// WAF Classic and shares operation names with WAF v2, so a mistargeted entry
// would let Classic's operations certify capability rows declared against v2.
func TestServiceAliases_areUniqueAndWellFormed(t *testing.T) {
	if len(serviceAliases) == 0 {
		t.Fatal("serviceAliases is empty — this test would pass while proving nothing")
	}

	seen := map[string]bool{}
	for i, alias := range serviceAliases {
		if alias.OvercastService == "" || alias.OvercastService != strings.ToLower(alias.OvercastService) {
			t.Errorf("serviceAliases[%d] maps %q to %q, which is not a lower-case service key",
				i, alias.ModelService, alias.OvercastService)
		}
		if seen[alias.ModelService] {
			t.Errorf("serviceAliases lists %q twice; overcastService binary-searches this table and would return whichever copy it landed on",
				alias.ModelService)
		}
		seen[alias.ModelService] = true
	}
}

// TestServiceAliases_areStrictlySorted pins the contract registry_data.go states
// in a comment and nothing enforced: "serviceAliases must stay strictly sorted
// by ModelService for binary search".
//
// overcastService resolves every classification through sort.Search over this
// slice. An out-of-order entry does not fail loudly — the search simply misses
// it, so one service's operations quietly stop resolving to their Overcast key
// while every other alias keeps working.
func TestServiceAliases_areStrictlySorted(t *testing.T) {
	for i := 1; i < len(serviceAliases); i++ {
		if serviceAliases[i-1].ModelService >= serviceAliases[i].ModelService {
			t.Errorf("serviceAliases is not strictly sorted at index %d: %q then %q — overcastService binary-searches it, so the later entry is unreachable",
				i, serviceAliases[i-1].ModelService, serviceAliases[i].ModelService)
		}
	}
}

// TestServiceAliases_areNotIdentityMappings asserts no entry maps a name to
// itself.
//
// overcastService returns its argument unchanged when the search misses, so an
// identity entry changes nothing and reads as though it does. That matters here
// more than it usually would: the table is the record of *which* modeled names
// are not Overcast keys, and an entry saying "x is x" invites the next reader to
// add the one that matters as a comment instead.
//
// The stronger rule — that an alias may not name a target that is itself an
// aliased identity — is deliberately not asserted, because WAF needs exactly
// that. "wafv2" maps to the key "waf", while the modeled "waf" identity (WAF
// Classic) is diverted to "waf-classic"; the pair is a swap rather than a chain,
// and it is safe because resolution is applied once, to a modeled name.
func TestServiceAliases_areNotIdentityMappings(t *testing.T) {
	for i, alias := range serviceAliases {
		if alias.ModelService == alias.OvercastService {
			t.Errorf("serviceAliases[%d] maps %q to itself — the entry does nothing", i, alias.ModelService)
		}
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
