package router

// preflight_region.go — "your resources are in another region".
//
// GET /_overcast/preflight/region?kind=<kind>
//
// # The failure this exists for
//
// A developer's tooling is pointed at one region and their console at another.
// The two never mention each other: the CLI deploys into ap-southeast-2
// because AWS_REGION says so — and AWS_REGION wins over AWS_DEFAULT_REGION,
// which is how the mismatch usually arises without anyone choosing it — while
// the console lists us-east-1 because that is where it starts. What the
// developer sees is an empty list beside an emulator that is plainly running
// and answering. Nothing on that screen is wrong, and nothing on it is about
// regions, so the two readings left are "Overcast lost my stacks" and "my
// deploy silently did nothing". Both are wrong, and both are expensive: this
// has cost real time on this project, and the AWS CLI wrapper carries its own
// warning about the same trap (see scripts/awslocal.sh's header).
//
// Overcast is the one process that can see both sides of it. Nothing else in
// the picture knows where the data is *and* which region the reader is looking
// at, so nothing else can say the one sentence that ends the search:
//
//	No stacks in us-east-1. There are 3 in ap-southeast-2.
//
// # Why it only ever fires on a matched symptom
//
// The check answers with an advisory only when the selected region is empty
// and another region is not. An account with nothing in it anywhere — a fresh
// emulator, or one whose in-memory state a restart discarded — gets silence,
// and so does a region that has resources in it. That restraint is the whole
// design: a preflight check that fires on a healthy setup teaches people to
// skip past it, and they do not learn to skip past it one check at a time.
// The rule is therefore enforced here rather than left to each caller, so a
// second console surface cannot get it subtly wrong on its own.
//
// # Why not a header on the AWS API
//
// Because it is not a fidelity claim about a response. x-emulator-unsupported
// and x-overcast-emulation-limitation both describe what Overcast will not do
// with a request it just answered (see internal/protocol/limitation.go); this
// describes where the caller's data is, which is a true and complete answer as
// far as the AWS API is concerned — a region-scoped List really is empty, and
// real AWS would say the same. So it lives on Overcast's own channel, talking
// to Overcast's own console, and no SDK-visible field changes shape.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// preflightRegionKind describes one thing a console list page can be empty of.
//
// The region trap is not CloudFormation's: every list page in the console is
// region-scoped and every one of them can come back empty for this reason. So
// a kind is data, not code — a namespace to read and, where the records
// outlive the resources they describe, a predicate saying which of them still
// count. Adding the fourth costs a row.
type preflightRegionKind struct {
	// kind is the name the console asks for. It names the *page's* subject
	// rather than the service, because two pages of the same service can be
	// empty for different reasons and would want separate answers.
	kind string

	// namespace is the state.Store namespace holding one record per resource,
	// keyed by serviceutil.RegionKey. That key shape is what makes the whole
	// check cheap: the per-region tally falls out of the keys themselves.
	namespace string

	// counts reports whether a stored record still represents something a list
	// page would show. nil means every record counts, which is the ordinary
	// case: almost every service deletes the record when the resource goes.
	//
	// A kind that needs this is a kind whose records outlive their resources,
	// and getting it wrong is worse than not having the check: an advisory
	// that sends someone to a region full of tombstones costs them the trip
	// and the credibility of every advisory after it.
	counts func(record string) bool
}

// preflightRegionKinds is the registry. A kind belongs here once two things
// are true of it: the console has a list page that shows exactly the records
// this namespace holds for the selected region, and the record's own lifetime
// matches the resource's (or `counts` reconciles the difference).
//
// Kinds deliberately absent rather than forgotten:
//
//   - S3 buckets. The namespace is not region-keyed, because a bucket name is
//     global on AWS and Overcast follows that. There is no per-region tally to
//     take, and inventing one would answer a question S3 does not ask.
//   - Anything whose console page filters or joins beyond "the records in this
//     namespace" — the count would not be the number the page failed to show,
//     and a count that disagrees with the page is a worse advisory than none.
var preflightRegionKinds = []preflightRegionKind{
	{
		kind:      "cloudformation-stacks",
		namespace: "cfn:stacks",
		// A deleted stack keeps its record: AWS's ListStacks reports
		// DELETE_COMPLETE stacks and Overcast matches that, so the console's
		// stack list filters them out client-side. This predicate is that same
		// filter, and it has to stay that same filter — a region holding only
		// deleted stacks shows an empty list too, and advising someone to go
		// there sends them from one empty page to another.
		counts: func(record string) bool {
			var stack struct {
				StackStatus string `json:"StackStatus"`
			}
			if json.Unmarshal([]byte(record), &stack) != nil {
				// A record that will not decode is a record the list page
				// could not have rendered either — services skip malformed
				// rows rather than failing the read, so this skips it too.
				return false
			}
			return stack.StackStatus != "DELETE_COMPLETE"
		},
	},
	{
		kind:      "sqs-queues",
		namespace: "sqs:queues",
	},
	{
		kind:      "lambda-functions",
		namespace: "lambda:functions",
	},
}

// preflightRegionKindByName finds a kind by the name the console asked for.
// A linear scan over a handful of rows is cheaper than the map that would
// otherwise have to be kept in step with the slice.
func preflightRegionKindByName(name string) (preflightRegionKind, bool) {
	for _, kind := range preflightRegionKinds {
		if kind.kind == name {
			return kind, true
		}
	}
	return preflightRegionKind{}, false
}

// preflightRegionElsewhere is one other region that holds resources of the
// kind the caller found none of.
type preflightRegionElsewhere struct {
	Region string `json:"region"`
	Count  int    `json:"count"`
}

// preflightRegionResponse answers "is this page empty because I am in the
// wrong region?".
//
// Elsewhere is empty whenever there is nothing to say — including when Count
// is non-zero, which is the case where the caller has no symptom to explain.
// A caller therefore renders on `elsewhere.length > 0` and needs no rule of
// its own.
type preflightRegionResponse struct {
	Kind   string `json:"kind"`
	Region string `json:"region"`
	// Count is what the selected region holds. It is reported even when it is
	// the reason there is no advisory, so a caller can tell "we looked and you
	// are fine" from "we did not look".
	Count     int                        `json:"count"`
	Elsewhere []preflightRegionElsewhere `json:"elsewhere"`
}

// newPreflightRegionHandler serves the region advisory for one kind.
//
// # Cost
//
// One read of one namespace, per call. The per-region breakdown comes out of
// the keys, which already carry the region as their first segment
// (serviceutil.RegionKey), so there is no fan-out over regions and no list of
// AWS regions anywhere in this file — Overcast only ever names a region some
// record is actually stored under.
//
// A kind with no predicate reads keys alone (state.Store.List), which copies
// no record bodies at all. A kind with one has to look at the records, so it
// reads key-value pairs (Scan) and decodes the single field it needs. Either
// way the work is the same order as the region-scoped list that just came back
// empty, and it is only ever done for a page that came back empty: the console
// does not call this otherwise.
func newPreflightRegionHandler(cfg *config.Config, store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, known := preflightRegionKindByName(r.URL.Query().Get("kind"))
		if !known {
			// A kind the server does not know is a console bug. Answering 200
			// with an empty advisory would bury it: the check would simply
			// never fire again, and nothing would ever say why.
			writePreflightError(w, http.StatusBadRequest, "UnknownKind",
				"No region preflight is registered for that resource kind.")
			return
		}

		selected := middleware.RegionFromContext(r.Context(), cfg.Region)
		tally, err := tallyRegions(r.Context(), store, kind, cfg.Region)
		if err != nil {
			writePreflightError(w, http.StatusInternalServerError, "PreflightUnavailable",
				"The state store could not be read to compare regions.")
			return
		}

		resp := preflightRegionResponse{
			Kind:      kind.kind,
			Region:    selected,
			Count:     tally[selected],
			Elsewhere: []preflightRegionElsewhere{},
		}
		if resp.Count == 0 {
			resp.Elsewhere = elsewhere(tally, selected)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort HTTP write.
	}
}

// tallyRegions counts, per region, the records of one kind the store holds.
//
// Records whose key carries no region prefix are attributed to defaultRegion,
// matching serviceutil.ScanRegions. They are the ones written before a
// namespace became region-scoped, and the alternative — a region named "" — is
// one no console could select and no advisory could honestly point at.
func tallyRegions(ctx context.Context, store state.Store, kind preflightRegionKind, defaultRegion string) (map[string]int, error) {
	tally := make(map[string]int)
	countKey := func(key string) {
		region, _ := serviceutil.SplitRegionKey(key)
		if region == "" {
			region = defaultRegion
		}
		tally[region]++
	}

	if kind.counts == nil {
		keys, err := store.List(ctx, kind.namespace, "")
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			countKey(key)
		}
		return tally, nil
	}

	records, err := store.Scan(ctx, kind.namespace, "")
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if kind.counts(record.Value) {
			countKey(record.Key)
		}
	}
	return tally, nil
}

// elsewhere returns every region except the selected one, fullest first —
// tallyRegions only ever records a region it counted something in, so every
// entry holds at least one. Ordering is the advisory's one piece of judgement:
// with several candidates, the region holding the most is the likeliest to be
// the one the developer's tooling has been deploying into. Ties break by name
// so the sentence a reader sees does not change between two identical requests.
func elsewhere(tally map[string]int, selected string) []preflightRegionElsewhere {
	out := make([]preflightRegionElsewhere, 0, len(tally))
	for region, count := range tally {
		if region == selected {
			continue
		}
		out = append(out, preflightRegionElsewhere{Region: region, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Region < out[j].Region
	})
	return out
}

// writePreflightError answers with a stable code a caller can branch on plus a
// sentence for whoever ends up reading it in a network tab, mirroring the
// shape internal/bff already uses for its own non-AWS errors.
func writePreflightError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(struct { //nolint:errcheck // best-effort HTTP write.
		Error   string `json:"error"`
		Message string `json:"message"`
	}{Error: code, Message: message})
}
