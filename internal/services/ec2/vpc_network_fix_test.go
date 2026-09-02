package ec2

// vpc_network_fix_test.go — health must not send an operator to a command that
// will refuse them (#1584).
//
// `overcast network reset` reads the internet-gateway fact off the network
// itself and declines every network that does not carry one: without it there
// is no way to tell an isolated bridge from a gateway-attached one, and
// rebuilding on a guess writes a state nothing chose. That is exactly the
// population an upgrade produces — so for those, naming that command in the
// health advisory is naming a refusal.

import (
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
)

func TestVPCNetworkFix_namesTheResetForANetworkTheCLICanJudge(t *testing.T) {
	info := &docker.NetworkInspect{
		Name:   "overcast-vpc-vpc-1",
		Labels: map[string]string{docker.LabelGatewayAttached: "true"},
	}
	if got := vpcNetworkFix(info); got != "overcast network reset overcast-vpc-vpc-1" {
		t.Errorf("fix = %q, want the reset command", got)
	}
}

// "false" is a recorded fact like any other: the CLI can judge it, so the reset
// is still the right answer.
func TestVPCNetworkFix_aRecordedFalseIsStillARecordedFact(t *testing.T) {
	info := &docker.NetworkInspect{
		Name:   "overcast-vpc-vpc-1",
		Labels: map[string]string{docker.LabelGatewayAttached: "false"},
	}
	if got := vpcNetworkFix(info); !strings.HasPrefix(got, "overcast network reset") {
		t.Errorf("fix = %q, want the reset command", got)
	}
}

func TestVPCNetworkFix_sendsALegacyNetworkToTheDaemonInstead(t *testing.T) {
	info := &docker.NetworkInspect{
		Name:   "overcast-vpc-vpc-old",
		Labels: map[string]string{docker.LabelManaged: "true"},
	}
	got := vpcNetworkFix(info)
	if strings.HasPrefix(got, "overcast network reset") {
		t.Fatalf("fix = %q, but that command declines a network with no recorded gateway state", got)
	}
	if !strings.Contains(got, "restart") {
		t.Errorf("fix = %q, want it to name the startup reconcile as the repair", got)
	}
	if !strings.Contains(got, "predates the recorded gateway state") {
		t.Errorf("fix = %q, want it to say why the reset cannot help", got)
	}
}

func TestVPCNetworkFix_hasNothingToSayAboutNoNetwork(t *testing.T) {
	if got := vpcNetworkFix(nil); got != "" {
		t.Errorf("fix = %q, want empty", got)
	}
}
