package router

// advisories_egress_test.go — the advisory #1575 promised and did not ship
// (#1585).
//
// `OVERCAST_VPC_EGRESS=none` is not set to change how a stack feels; it is set
// to *prove* the stack has no hidden external dependency. On Docker Desktop,
// with Overcast on the host, it cannot keep that promise: containers reach the
// Lambda Runtime API at the host's own address, an internal control plane would
// sever it, and every invocation would strand at INIT. Overcast makes the trade
// deliberately and leaves that one network routable — so every container keeps
// a default route through it.
//
// That was said once, at WARN, at boot, and carried in a `reason` string inside
// /_overcast/health that nothing rendered as a problem. The Metrics and Health
// page showed nothing at all.

import (
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

// hostVetoReason is what dataplane.ControlPlaneInternal writes when the mode
// asked for isolation and the host would not take it. The ", overridden: "
// marker is the contract between that decision and this rule.
const hostVetoReason = "OVERCAST_VPC_EGRESS=none, overridden: an internal control plane would sever the Runtime API on this host"

// noneOnDockerDesktop is what Probe records on the host this advisory exists
// for: both data planes isolated as asked, the control plane overridden.
func noneOnDockerDesktop() advisoryInput {
	return advisoryInput{
		VPCEgress: config.VPCEgressNone,
		ControlPlane: docker.NetworkDecision{
			Network: "overcast_control", Internal: false, Reason: hostVetoReason,
		},
		Networks: []docker.NetworkStatus{
			{Name: "overcast", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
			{Name: "overcast-vpc-vpc-1", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
			{Name: "overcast_control", Internal: false, Reason: hostVetoReason},
		},
	}
}

func TestCheckEgressNotWithheld_firesWhenTheControlPlaneStaysRoutable(t *testing.T) {
	a := checkEgressNotWithheld(noneOnDockerDesktop())
	if a == nil {
		t.Fatal("no advisory: `none` left every container a route out and said nothing")
	}
	if a.Code != advisoryCodeEgressNotWithheld {
		t.Errorf("Code = %q, want %q", a.Code, advisoryCodeEgressNotWithheld)
	}
	if a.Severity != advisorySeverityWarning {
		t.Errorf("Severity = %q, want %q", a.Severity, advisorySeverityWarning)
	}
	// The three things an operator has to be able to act on: which network,
	// what imposed it, and what to change.
	if !strings.Contains(a.Detail, "overcast_control") {
		t.Errorf("Detail does not name the network that was left routable: %s", a.Detail)
	}
	if !strings.Contains(a.Detail, "on this host reach") {
		t.Errorf("Detail does not say the host is what imposed this: %s", a.Detail)
	}
	if !strings.Contains(a.Detail, "container") || !strings.Contains(a.Detail, "native Linux") {
		t.Errorf("Detail does not name the way to get the whole of `none`: %s", a.Detail)
	}
	if a.DocsPath == "" {
		t.Error("DocsPath is empty; the console card has nowhere to send anyone")
	}
}

// A host that can deliver `none` delivers it, and hears nothing. An advisory
// that fires on the working case is one nobody reads.
func TestCheckEgressNotWithheld_silentWhenTheControlPlaneIsIsolated(t *testing.T) {
	in := noneOnDockerDesktop()
	in.ControlPlane = docker.NetworkDecision{
		Network: "overcast_control", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none",
	}
	for i := range in.Networks {
		in.Networks[i].Internal = true
		in.Networks[i].Reason = "OVERCAST_VPC_EGRESS=none"
	}
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v, want none: every network is isolated as asked", a)
	}
}

// The operator pinned the deprecated variable, so dataplane.ControlPlaneInternal
// returned before it ever consulted the host. The advisory is still right —
// egress is not withheld — but it must not explain itself with a probe result
// nobody obtained. Asserting an unchecked fact is what this release is about.
func TestCheckEgressNotWithheld_doesNotBlameTheHostForAnOperatorOverride(t *testing.T) {
	in := noneOnDockerDesktop()
	in.ControlPlane = docker.NetworkDecision{
		Network: "overcast_control", Internal: false, Reason: "OVERCAST_CONTROL_PLANE_INTERNAL=false",
	}

	a := checkEgressNotWithheld(in)
	if a == nil {
		t.Fatal("no advisory: egress is not withheld, whatever chose that")
	}
	if strings.Contains(a.Detail, "on this host reach") || strings.Contains(a.Detail, "native Linux") {
		t.Errorf("Detail blames the host for a decision the host was never asked about: %s", a.Detail)
	}
	if !strings.Contains(a.Detail, "OVERCAST_CONTROL_PLANE_INTERNAL") {
		t.Errorf("Detail does not name the setting that actually decided this: %s", a.Detail)
	}
	if !strings.Contains(a.Detail, "asked for, not imposed") {
		t.Errorf("Detail does not distinguish an override from a shortfall: %s", a.Detail)
	}
}

// A reason carrying neither the marker nor a recognisable pin still fires the
// advisory — the outcome is what it is about — but it invents no cause.
func TestCheckEgressNotWithheld_firesWithoutInventingACause(t *testing.T) {
	in := noneOnDockerDesktop()
	in.ControlPlane = docker.NetworkDecision{Network: "overcast_control", Internal: false}

	a := checkEgressNotWithheld(in)
	if a == nil {
		t.Fatal("no advisory: the control plane is routable under `none`")
	}
	if strings.Contains(a.Detail, "on this host reach") {
		t.Errorf("Detail asserts a host fact with no reason to support it: %s", a.Detail)
	}
}

// Only a mode that promises to withhold egress can fail to keep the promise.
// `open` promises the opposite, so it fires nothing however the host is
// arranged — and neither does the zero value, which Load never produces and
// dataplane.EgressMode reads as `open`.
func TestCheckEgressNotWithheld_neverFiresForOpen(t *testing.T) {
	for _, mode := range []config.VPCEgressMode{config.VPCEgressOpen, ""} {
		t.Run(string(mode), func(t *testing.T) {
			in := noneOnDockerDesktop()
			in.VPCEgress, in.PlacementEnforced = mode, false
			if a := checkEgressNotWithheld(in); a != nil {
				t.Fatalf("advisory = %+v, want none for mode %q", a, mode)
			}
		})
	}
}

// No decision recorded means Docker was never probed, which is an absence
// rather than a problem.
func TestCheckEgressNotWithheld_silentWithNoDecisionRecorded(t *testing.T) {
	in := noneOnDockerDesktop()
	in.ControlPlane = docker.NetworkDecision{}
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v, want none: Docker was never probed", a)
	}
}

// The whole point of the decision being carried separately: an empty network
// report does not silence a rule about configuration.
func TestCheckEgressNotWithheld_doesNotNeedTheNetworkToStillExist(t *testing.T) {
	in := noneOnDockerDesktop()
	in.Networks = nil
	if a := checkEgressNotWithheld(in); a == nil {
		t.Fatal("no advisory with an empty network report; the decision is what this rule reads")
	}
}

// The sequence that made this a finding. `overcast network reset
// overcast_control` removes the network before rebuilding it, this daemon sees
// the destroy and forgets the record (#1583), and nothing re-records it until
// the next probe. Keying the rule off the record meant the advisory switched
// itself off while the shortfall it reports was entirely unchanged.
func TestCheckEgressNotWithheld_survivesTheNetworkBeingForgotten(t *testing.T) {
	tr := docker.NewTracker()
	tr.RecordNetworks([]docker.NetworkStatus{
		{Name: "overcast", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
		{Name: "overcast_control", Internal: false, Reason: hostVetoReason},
	})

	tr.ForgetNetwork("overcast_control")

	snap := tr.Snapshot()
	for _, n := range snap.Networks {
		if n.Name == "overcast_control" {
			t.Fatal("the forgotten network is still listed; this test is not exercising the window")
		}
	}

	in := advisoryInput{VPCEgress: config.VPCEgressNone, Networks: snap.Networks}
	for _, d := range snap.Decisions {
		if d.Network == "overcast_control" {
			in.ControlPlane = d
		}
	}
	if a := checkEgressNotWithheld(in); a == nil {
		t.Fatal("the advisory stopped firing because the network was rebuilt; the shortfall is unchanged")
	}
}

// The same shortfall, through the other route that can now write to the
// tracker mid-run: the Docker watcher re-verifying a network on its `create`
// event (#1599).
//
// A drifted control plane records what it *is* — internal — on the network
// entry, which is right and is what an operator needs to see. The decision has
// to keep saying what this run resolved, or `controlPlaneRoutable` reads the
// observation, decides the plane is isolated after all, and switches this rule
// off while the shortfall it reports is entirely unchanged. That is the same
// failure #1583's ForgetNetwork produced from one route over, which is why the
// ControlPlane field exists at all.
func TestCheckEgressNotWithheld_survivesADriftedControlPlaneRecordedFromAnEvent(t *testing.T) {
	tr := docker.NewTracker()
	tr.RecordDecisions([]docker.NetworkDecision{
		{Network: "overcast_control", Internal: false, Reason: hostVetoReason},
	})
	tr.RecordNetworks([]docker.NetworkStatus{
		{Name: "overcast", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
		{Name: "overcast_control", Internal: false, Reason: hostVetoReason},
	})

	// Somebody rebuilds the control plane by hand as `--internal`; the create
	// event re-verifies it and records the drift.
	tr.RecordNetworks([]docker.NetworkStatus{{
		Name: "overcast_control", Internal: true, Reason: hostVetoReason,
		Mismatch: []docker.NetworkFieldDiff{{Field: "internal", Want: "false", Got: "true"}},
		Drift:    "network is not in the configured state (internal: want false, got true)",
		Fix:      "overcast network reset overcast_control",
	}})

	snap := tr.Snapshot()
	in := advisoryInput{VPCEgress: config.VPCEgressNone, Networks: snap.Networks}
	for _, d := range snap.Decisions {
		if d.Network == "overcast_control" {
			in.ControlPlane = d
		}
	}
	if in.ControlPlane.Internal {
		t.Fatal("the decision took the drifted network's isolation; this rule reads it and is now silent")
	}
	if a := checkEgressNotWithheld(in); a == nil {
		t.Fatal("the advisory stopped firing because somebody edited the network; the shortfall is unchanged")
	}
}

// The generator has to call it, or the rule is dead code with passing tests.
func TestComputeAdvisories_includesTheEgressRule(t *testing.T) {
	got := computeAdvisories(noneOnDockerDesktop())
	for _, a := range got {
		if a.Code == advisoryCodeEgressNotWithheld {
			return
		}
	}
	t.Fatalf("advisories = %+v, want %s among them", got, advisoryCodeEgressNotWithheld)
}

// ── `routed`, the other withholding mode (#1571) ─────────────────────────────
//
// It fails the same way `none` does — a control plane that could not be
// isolated — and one way `none` cannot: `routed` leaves the shared data plane
// routable, so a VPC-placed container that also lands on it takes a default
// route its subnet's route table never granted. One rule answers both, which
// is why the cases live beside `none`'s.

// routedOnDockerDesktop is what a native Windows or macOS host produces: the
// control plane overridden, and no resolver, so placement is not enforced.
func routedOnDockerDesktop() advisoryInput {
	return advisoryInput{
		VPCEgress:         config.VPCEgressRouted,
		PlacementEnforced: false,
		ControlPlane: docker.NetworkDecision{
			Network:  "overcast_control",
			Internal: false,
			Reason: "OVERCAST_VPC_EGRESS=routed, overridden: an internal control plane would sever " +
				"the Runtime API on this host",
		},
	}
}

func TestCheckEgressNotWithheld_routedNamesBothShortfalls(t *testing.T) {
	a := checkEgressNotWithheld(routedOnDockerDesktop())
	if a == nil {
		t.Fatal("no advisory: `routed` withheld nothing and said nothing")
	}
	if a.Code != advisoryCodeEgressNotWithheld {
		t.Errorf("Code = %q, want the one code both egress rules share", a.Code)
	}
	if a.Title != "OVERCAST_VPC_EGRESS=routed cannot withhold egress on this host" {
		t.Errorf("Title = %q; it must name the mode the operator set", a.Title)
	}
	for _, want := range []string{
		"overcast_control",   // which network
		"on this host reach", // and that the host imposed it
		"DNS resolver",       // the second shortfall, which is `routed`-only
		"0.0.0.0/0",          // what it costs
		"NAT gateway",
		"native Linux", // and the way out
	} {
		if !strings.Contains(a.Detail, want) {
			t.Errorf("Detail does not mention %q: %s", want, a.Detail)
		}
	}
	// One fix, once: both shortfalls have the same remedy here.
	if n := strings.Count(a.Detail, "Run Overcast in a container"); n != 1 {
		t.Errorf("the remedy is printed %d times, want once: %s", n, a.Detail)
	}
	if a.DocsPath != egressModeDocsPath {
		t.Errorf("DocsPath = %q, want the shared egress-modes path", a.DocsPath)
	}
}

// A host that can deliver `routed` delivers it and hears nothing — the case
// the live matrix in #1594 ran against, with Overcast in a container.
func TestCheckEgressNotWithheld_silentWhereRoutedCanKeepItsPromise(t *testing.T) {
	in := routedOnDockerDesktop()
	in.PlacementEnforced = true
	in.ControlPlane = docker.NetworkDecision{
		Network: "overcast_control", Internal: true, Reason: "OVERCAST_VPC_EGRESS=routed",
	}
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v, want none: the control plane is isolated and placement is enforced", a)
	}
}

// Each shortfall stands alone, and neither invents the other.
func TestCheckEgressNotWithheld_routedFiresOnEitherShortfallAlone(t *testing.T) {
	t.Run("placement only", func(t *testing.T) {
		// A native Linux daemon with no resolver: the control plane is fine.
		in := routedOnDockerDesktop()
		in.ControlPlane = docker.NetworkDecision{
			Network: "overcast_control", Internal: true, Reason: "OVERCAST_VPC_EGRESS=routed",
		}
		a := checkEgressNotWithheld(in)
		if a == nil {
			t.Fatal("an unenforced placement fired nothing")
		}
		if strings.Contains(a.Detail, "left routable") {
			t.Errorf("Detail blames the control plane, which was isolated: %s", a.Detail)
		}
		if !strings.Contains(a.Detail, "DNS resolver") {
			t.Errorf("Detail does not name the shortfall that fired: %s", a.Detail)
		}
	})

	t.Run("control plane only", func(t *testing.T) {
		in := routedOnDockerDesktop()
		in.PlacementEnforced = true
		a := checkEgressNotWithheld(in)
		if a == nil {
			t.Fatal("a routable control plane fired nothing")
		}
		if strings.Contains(a.Detail, "DNS resolver") {
			t.Errorf("Detail blames placement, which was enforced: %s", a.Detail)
		}
	})
}

// The asymmetry, asserted rather than assumed: `none` isolates the shared data
// plane too, so a container landing on it gains nothing and unenforced
// placement is not a shortfall there. Only `routed` reads the field.
func TestCheckEgressNotWithheld_noneIgnoresUnenforcedPlacement(t *testing.T) {
	in := noneOnDockerDesktop()
	in.PlacementEnforced = false
	in.ControlPlane = docker.NetworkDecision{
		Network: "overcast_control", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none",
	}
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v; unenforced placement is not a shortfall under `none`", a)
	}
}

// The operator pin, under `routed`, alongside the placement shortfall: the
// pin's fix is named without blaming the host for it, and the placement half
// still gets the remedy that actually addresses it.
func TestCheckEgressNotWithheld_routedSeparatesAPinFromTheHost(t *testing.T) {
	in := routedOnDockerDesktop()
	in.ControlPlane = docker.NetworkDecision{
		Network: "overcast_control", Internal: false, Reason: "OVERCAST_CONTROL_PLANE_INTERNAL=false",
	}

	a := checkEgressNotWithheld(in)
	if a == nil {
		t.Fatal("no advisory: egress is not withheld, whatever chose that")
	}
	if strings.Contains(a.Detail, "on this host reach") {
		t.Errorf("Detail blames the host for a decision it was never asked about: %s", a.Detail)
	}
	if !strings.Contains(a.Detail, "asked for, not imposed") ||
		!strings.Contains(a.Detail, "OVERCAST_CONTROL_PLANE_INTERNAL") {
		t.Errorf("Detail does not name the setting that decided this: %s", a.Detail)
	}
	// The placement shortfall is unrelated to the pin and keeps its own fix.
	if !strings.Contains(a.Detail, "native Linux") {
		t.Errorf("Detail drops the remedy for the placement shortfall: %s", a.Detail)
	}
}

// The generator has to call it for `routed` too.
func TestComputeAdvisories_includesTheEgressRuleForRouted(t *testing.T) {
	for _, a := range computeAdvisories(routedOnDockerDesktop()) {
		if a.Code == advisoryCodeEgressNotWithheld {
			return
		}
	}
	t.Fatal("computeAdvisories did not include the egress rule under `routed`")
}
