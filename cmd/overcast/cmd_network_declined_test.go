package main

// cmd_network_declined_test.go — what `overcast network reset` says about a
// network it is not entitled to judge (#1584).
//
// Declining is right: a per-VPC network from before Overcast recorded the
// internet-gateway fact gives this command no way to tell an isolated bridge
// from a gateway-attached one, and rebuilding on a guess would write a state
// nothing chose. The bug was the sentence. "Every network is already in the
// state this configuration asks for. Nothing to do." is the opposite claim, and
// an operator arrives here having been sent by the drift warning or the health
// advisory — so they read it as the fix having worked.

import (
	"bytes"
	"strings"
	"testing"
)

// declinedWording is the one sentence both the empty-plan path and the
// explicitly-named rebuild print, so a test that pins it pins both.
const declinedWording = "predates the recorded gateway state"

func TestNetworkReset_saysItCouldNotJudgeRatherThanNothingToDo(t *testing.T) {
	for _, args := range [][]string{
		{"reset", "--yes"},
		{"reset", "octest-vpc-vpc-old", "--yes"},
		{"reset", "--dry-run"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cleanNetworkEnv(t)
			d := newCLIDaemon(t)
			d.setInstance("this-instance")

			// As the daemon really made it, before the gateway fact was
			// written down. Both planes are absent, so this is the only
			// target and the plan is necessarily empty.
			n := vpcNetwork(t, "vpc-old", true, "this-instance")
			delete(n["Labels"].(map[string]string), "overcast.network.gateway")
			d.addNetwork(n)

			var out bytes.Buffer
			cmd := newNetworkCmd()
			cmd.SetOut(&out)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("network %v: %v", args, err)
			}

			got := out.String()
			if !strings.Contains(got, declinedWording) {
				t.Errorf("output does not say the network could not be judged:\n%s", got)
			}
			if strings.Contains(got, "Every network is already in the state this configuration asks for") {
				t.Errorf("output claims every network matches; this one was never compared:\n%s", got)
			}
			if d.saw("DELETE") || d.saw("POST /v1.45/networks/create") {
				t.Errorf("the network was rebuilt on a guess; calls: %v", d.calls)
			}
		})
	}
}

// The unqualified sentence is still right when it is right: nothing was
// declined, so nothing needs explaining.
func TestNetworkReset_stillSaysNothingToDoWhenThatIsWhy(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.setInstance("this-instance")
	// A VPC network that matches and carries the gateway fact: judged, and
	// found correct.
	d.addNetwork(vpcNetwork(t, "vpc-ok", true, "this-instance"))

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"reset", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network reset --dry-run: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Every network is already in the state this configuration asks for") {
		t.Errorf("output = %q, want the plain nothing-to-do wording", got)
	}
	if strings.Contains(got, declinedWording) {
		t.Errorf("output explains a refusal that did not happen:\n%s", got)
	}
}
