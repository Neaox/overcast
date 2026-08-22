//go:build ignore

// Script: route-reachability
// Probes a running Overcast instance at the AWS-modeled wire binding of every
// operation Overcast *declares* it implements, and reports the ones no AWS SDK
// can reach.
//
// Why this exists: `make aws-models-check` validates that capability rows name
// real AWS operations and that no modeled operation falls through to S3. Both
// gates are satisfied by an operation that answers 501 — so an implementation
// mounted on an Overcast-invented path or an invented X-Amz-Target prefix
// passes CI while being unreachable from any SDK (see #793 EventBridge
// Scheduler, #815 AWS Backup). This script closes that gap by asking the only
// question those gates do not: does the modeled binding reach a handler?
//
// The reusable half of this — the model index, request builder and verdict
// logic — lives in internal/routecheck so it is not re-derived. The same
// logic also runs on every PR, in-process and with no live instance required,
// as tests/integration/router/route_reachability_dev_test.go: this script
// remains the tool for a real (RC or local) instance, which is the only way
// to exercise the exact built binary/image rather than an in-process test
// server. See docs/plans/route-reachability-audit.md and
// .agents/skills/release/SKILL.md § Smoke for when to run each.
//
// Expectation set: internal/capabilities (the -tags dev snapshot). An operation
// Overcast declares Supported/Partial/Inert/WIP is expected to be reachable at
// its modeled binding. StatusUnsupported and DocOnly rows are excluded — a 501
// is the correct answer for those.
//
// Verdicts:
//
//	reachable    — a handler answered (2xx, or an AWS-shaped 4xx such as a
//	               validation or not-found error, which still proves the route)
//	shared-path  — answered only on a binding another Overcast service also
//	               models, so a route matched but the owner is unproven
//	unreachable  — 501 carrying x-emulator-unsupported on every modeled binding
//	unmodeled    — the capability names no operation in the pinned manifest
//	               (capgen's --check-model exemptions; not a routing fault)
//
// A "reachable" verdict proves a route matched, NOT that the right service
// answered. Where two AWS services share a path (AppConfig and AppRegistry both
// model POST /applications) only -show-body distinguishes them, which is what
// the shared-path verdict exists to prompt.
//
// Usage:
//
//	scripts/run-test-instance.sh --no-logs --name reach     # prints the API URL
//	go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:4570
//	go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:4570 -service appconfig -show-body
//	go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:4570 -json report.json
//
// The -tags dev is required: internal/capabilities is empty without it.
//
// Probes are unauthenticated beyond a syntactically valid SigV4 Authorization
// header carrying the modeled signing name — ownership and routing are decided
// from the credential scope, never from the signature, so no real signing is
// needed. An instance running with signature validation on would need genuinely
// signed requests.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/Neaox/overcast/internal/capabilities"
	"github.com/Neaox/overcast/internal/routecheck"
)

func main() {
	endpoint := flag.String("endpoint", "", "base URL of a running Overcast instance, e.g. http://127.0.0.1:4570")
	only := flag.String("service", "", "probe only this Overcast service key")
	jsonOut := flag.String("json", "", "also write the full report as JSON to this path")
	showBody := flag.Bool("show-body", false, "print the first bytes of each response body")
	timeout := flag.Duration("timeout", routecheck.DefaultProbeTimeout, "per-request timeout")
	flag.Parse()

	if *endpoint == "" {
		fatalf("set -endpoint to a running instance, e.g. -endpoint http://127.0.0.1:4570 " +
			"(start one with scripts/run-test-instance.sh --no-logs)")
	}

	if len(capabilities.AllCapabilities) == 0 {
		fatalf("no capabilities compiled in — rerun with -tags dev")
	}

	client := &http.Client{
		Timeout: *timeout,
		// One host, thousands of requests. Without a generous idle pool every
		// probe opens a fresh socket and Windows runs out of ephemeral ports
		// long before the sweep finishes.
		Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, IdleConnTimeout: 30 * time.Second},
	}
	ctx := context.Background()
	if err := waitReady(ctx, client, *endpoint); err != nil {
		fatalf("%v", err)
	}

	findings := routecheck.Sweep(ctx, client, *endpoint, *only, capabilities.AllCapabilities)
	if !*showBody {
		for i := range findings {
			for j := range findings[i].Probes {
				findings[i].Probes[j].Body = ""
			}
		}
	}

	report(findings, *showBody)
	if *jsonOut != "" {
		blob, err := json.MarshalIndent(findings, "", "  ")
		if err != nil {
			fatalf("marshal report: %v", err)
		}
		if err := os.WriteFile(*jsonOut, append(blob, '\n'), 0o644); err != nil {
			fatalf("write %s: %v", *jsonOut, err)
		}
		fmt.Printf("\nwrote %s\n", *jsonOut)
	}
}

// waitReady blocks until the instance answers, so a probe sweep started right
// after `docker run` reports routing faults rather than connection refusals.
func waitReady(ctx context.Context, client *http.Client, base string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/_overcast/health", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no instance answered %s/_overcast/health within 60s: %w", base, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// report prints a per-service summary, then the unreachable operations and the
// shared-binding ones — together, the list an auditor acts on.
func report(findings []routecheck.Finding, showBody bool) {
	type tally struct{ reachable, shared, unreachable, unmodeled int }
	byService := map[string]*tally{}
	for _, f := range findings {
		t := byService[f.Service]
		if t == nil {
			t = &tally{}
			byService[f.Service] = t
		}
		switch f.Verdict {
		case routecheck.VerdictReachable:
			t.reachable++
		case routecheck.VerdictShared:
			t.shared++
		case routecheck.VerdictUnreachable:
			t.unreachable++
		default:
			t.unmodeled++
		}
	}

	services := make([]string, 0, len(byService))
	for svc := range byService {
		services = append(services, svc)
	}
	sort.Strings(services)

	fmt.Printf("%-18s %10s %7s %12s %10s  %s\n", "SERVICE", "REACHABLE", "SHARED", "UNREACHABLE", "UNMODELED", "VERDICT")
	totalUnreachable, totalShared := 0, 0
	for _, svc := range services {
		t := byService[svc]
		totalUnreachable += t.unreachable
		totalShared += t.shared
		verdict := "ok"
		switch {
		case t.reachable == 0 && t.unreachable+t.shared > 0:
			verdict = "WHOLE SERVICE UNREACHABLE OR MISROUTED"
		case t.unreachable > 0:
			verdict = "partial gap"
		case t.shared > 0:
			verdict = "shared bindings — verify owner"
		}
		fmt.Printf("%-18s %10d %7d %12d %10d  %s\n", svc, t.reachable, t.shared, t.unreachable, t.unmodeled, verdict)
	}

	fmt.Printf("\n%d declared operations are unreachable at their modeled binding.\n", totalUnreachable)
	fmt.Printf("%d more answer only on a binding another AWS service also models — read the body to see who answered.\n", totalShared)

	printDetail := func(heading, want string) {
		var shown bool
		for _, f := range findings {
			if f.Verdict != want {
				continue
			}
			if !shown {
				fmt.Println("\n" + heading)
				shown = true
			}
			for _, p := range f.Probes {
				line := fmt.Sprintf("  %s/%s [%s]  %s -> %d", f.Service, f.Operation, f.Status, p.Binding, p.Status)
				if p.Error != "" {
					line += "  ERROR " + p.Error
				}
				fmt.Println(line)
				if showBody && p.Body != "" {
					fmt.Println("      " + p.Body)
				}
			}
		}
	}
	printDetail("UNREACHABLE DETAIL", routecheck.VerdictUnreachable)
	printDetail("SHARED-BINDING DETAIL (rerun with -show-body to see which service answered)", routecheck.VerdictShared)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "route-reachability: "+format+"\n", args...)
	os.Exit(1)
}
