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
// `open` promises the opposite, and `routed` has its own rule (#1571).
func TestCheckEgressNotWithheld_onlyFiresForNone(t *testing.T) {
	for _, mode := range []config.VPCEgressMode{config.VPCEgressOpen, config.VPCEgressRouted, ""} {
		t.Run(string(mode), func(t *testing.T) {
			in := noneOnDockerDesktop()
			in.VPCEgress = mode
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
