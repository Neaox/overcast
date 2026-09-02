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

// noneOnDockerDesktop is what Probe records on the host this advisory exists
// for: both data planes isolated as asked, the control plane overridden.
func noneOnDockerDesktop() advisoryInput {
	return advisoryInput{
		VPCEgress:      config.VPCEgressNone,
		ControlNetwork: "overcast_control",
		Networks: []docker.NetworkStatus{
			{Name: "overcast", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
			{Name: "overcast-vpc-vpc-1", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
			{Name: "overcast_control", Internal: false,
				Reason: "OVERCAST_VPC_EGRESS=none, overridden: an internal control plane would sever the Runtime API on this host"},
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
	// The two things an operator has to be able to act on: which network, and
	// what to change.
	if !strings.Contains(a.Detail, "overcast_control") {
		t.Errorf("Detail does not name the network that was left routable:\n%s", a.Detail)
	}
	if !strings.Contains(a.Detail, "container") || !strings.Contains(a.Detail, "native Linux") {
		t.Errorf("Detail does not name the way to get the whole of `none`:\n%s", a.Detail)
	}
	if a.DocsPath == "" {
		t.Error("DocsPath is empty; the console card has nowhere to send anyone")
	}
}

// A host that can deliver `none` delivers it, and hears nothing. An advisory
// that fires on the working case is one nobody reads.
func TestCheckEgressNotWithheld_silentWhenTheControlPlaneIsIsolated(t *testing.T) {
	in := noneOnDockerDesktop()
	for i := range in.Networks {
		in.Networks[i].Internal = true
		in.Networks[i].Reason = "OVERCAST_VPC_EGRESS=none"
	}
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v, want none: every network is isolated as asked", a)
	}
}

// Only a mode that promises to withhold egress can fail to keep the promise.
// `open` promises the opposite, and `routed` has its own rule (#1571) with a
// second failure mode this one does not share.
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

// Absence is not a shortfall. No networks reported means Docker was never
// probed, which is indistinguishable from a machine with no containers.
func TestCheckEgressNotWithheld_silentWithNothingReported(t *testing.T) {
	in := noneOnDockerDesktop()
	in.Networks = nil
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v, want none: nothing was reported to judge", a)
	}

	in = noneOnDockerDesktop()
	in.ControlNetwork = ""
	if a := checkEgressNotWithheld(in); a != nil {
		t.Fatalf("advisory = %+v, want none: no control-plane name to look for", a)
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
