//go:build dev

package router_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/capabilities"
	"github.com/overcast-sh/overcast/internal/routecheck"
	"github.com/overcast-sh/overcast/tests/helpers"
)

// TestAllDeclaredCapabilitiesAreReachable is the enforcement teeth
// docs/plans/route-reachability-audit.md asked for (issue #1199) for
// AGENTS.md's "verify no custom endpoints were introduced" checklist step,
// which had none: every one of the audit's Category-A findings — a service
// mounted on an Overcast-invented path or X-Amz-Target prefix, so every AWS
// SDK call reaches the 501 fallback while a working implementation sits
// behind a URL no client will ever send — passed that checklist step
// unnoticed, because "verify" named no check a machine could run.
//
// This test runs scripts/route-reachability.go's sweep in-process, against a
// full-service httptest server instead of a live instance, so it needs
// neither Docker nor a network and runs on every PR under -tags slim,dev
// rather than only when someone remembers to run the script by hand before a
// release. See internal/routecheck's package doc for why an in-process sweep
// is a faithful substitute for a live one: both drive the exact same request
// over real HTTP, through the real router, middleware and dispatch mounts —
// this test differs from the CLI only in *where* the requests land.
//
// What this deliberately does not re-check: internal/router's
// modelbinding_dev_test.go and friends (docs/plans/manifest-enforcement.md)
// already assert, from the routing table alone, that a modeled REST binding
// has a registered route. That is a necessary condition for reachability, not
// a sufficient one — a chi sub-router can own a whole subtree and silently
// answer a path it does not itself serve (exactly what happened to AppConfig
// under AppRegistry's /applications mount, #854) even though the table says a
// route exists at that path. Only issuing the request and reading the
// response proves the wire actually reaches the declaring service's own
// handler, which is what this test does and the table-only gates cannot.
func TestAllDeclaredCapabilitiesAreReachable(t *testing.T) {
	// Given: every default-registered service (the same set a real user gets;
	// see helpers.WithServiceSubset's own doc on why narrowing it would test a
	// shape no user ever sees).
	srv := helpers.NewTestServer(t)

	// A loopback in-process server either answers immediately or has a real
	// bug; a short bound keeps a genuinely hung handler from stalling the
	// whole suite instead of failing it.
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// capabilities.Default.All() — the live registry every service's init()
	// populates — rather than the generated capabilities.AllCapabilities
	// snapshot: NewTestServer links every service package, so the registry is
	// already exact, and reading it directly means a newly declared
	// capability is swept immediately rather than only after the next
	// `make generate-caps`.
	findings := routecheck.Sweep(ctx, client, srv.URL, "", capabilities.Default.All())

	// Then: no capability Overcast declares Supported/Partial/Inert/WIP may be
	// unreachable at every one of its modeled bindings. "shared-path" and
	// "unmodeled" are not asserted here — the audit's own "Shared bindings
	// that are not faults" section is why: several are shared by design
	// (tagsDispatch, v2APIsDispatch), and telling those apart from a real
	// fault needs the -show-body judgement call the audit made by hand, not
	// a mechanical rule this gate can apply for every future addition.
	var unreachable []string
	for _, f := range findings {
		if f.Verdict != routecheck.VerdictUnreachable {
			continue
		}
		var bindings []string
		for _, p := range f.Probes {
			line := fmt.Sprintf("%s -> %d", p.Binding, p.Status)
			if p.Error != "" {
				line += " (error: " + p.Error + ")"
			}
			bindings = append(bindings, line)
		}
		unreachable = append(unreachable, fmt.Sprintf("%s/%s [%s]: %s",
			f.Service, f.Operation, f.Status, strings.Join(bindings, "; ")))
	}
	sort.Strings(unreachable)

	if len(unreachable) > 0 {
		t.Fatalf("%d declared capabilities are unreachable at every modeled binding — "+
			"either the route is mounted somewhere no AWS SDK will ever send a request "+
			"(see docs/plans/route-reachability-audit.md), or the capability's declared "+
			"Status overclaims what the implementation actually serves:\n%s",
			len(unreachable), strings.Join(unreachable, "\n"))
	}
}
