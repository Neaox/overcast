package router

// advisories_unverified_test.go — the network-state advisory has to be able to
// report a network nobody managed to compare (#1582).
//
// docker.EnsureNetwork now returns a status carrying a Drift and no Mismatch
// when the daemon would not answer. Rendering that through the field list alone
// produced "overcast ()." — this advisory saying nothing at the moment it has
// something to say.

import (
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
)

func TestCheckNetworkStateMismatch_reportsANetworkThatWasNeverCompared(t *testing.T) {
	a := checkNetworkStateMismatch([]docker.NetworkStatus{{
		Name:  "overcast_control",
		Drift: "could not be verified against this configuration: docker GET /v1.45/networks: 500",
		Fix:   "overcast network status",
	}})
	if a == nil {
		t.Fatal("no advisory: an unverified network reported as fine is the #1564 lie")
	}
	if !strings.Contains(a.Detail, "could not be verified") {
		t.Errorf("Detail does not carry the reason:\n%s", a.Detail)
	}
	if strings.Contains(a.Detail, "overcast_control ().") {
		t.Errorf("Detail rendered an empty field list:\n%s", a.Detail)
	}
	// --dry-run belongs to the rebuild, not to a read-only command. Offering it
	// beside `overcast network status` sends an operator to a usage error.
	if strings.Contains(a.Detail, "--dry-run") {
		t.Errorf("Detail offers --dry-run beside a command that does not take it:\n%s", a.Detail)
	}
}

// The ordinary case keeps the field list and keeps the flag hint.
func TestCheckNetworkStateMismatch_keepsTheFieldListAndTheDryRunHint(t *testing.T) {
	a := checkNetworkStateMismatch([]docker.NetworkStatus{{
		Name:     "overcast",
		Mismatch: []docker.NetworkFieldDiff{{Field: "internal", Want: "false", Got: "true"}},
		Attached: []string{"someone-elses-container"},
		Drift:    "network is not in the configured state (internal: want false, got true)",
		Fix:      "overcast network reset overcast",
	}})
	if a == nil {
		t.Fatal("no advisory for a drifted network with a container on it")
	}
	if !strings.Contains(a.Detail, "internal: want false, got true") {
		t.Errorf("Detail lost the field list:\n%s", a.Detail)
	}
	if !strings.Contains(a.Detail, "--dry-run") {
		t.Errorf("Detail lost the --dry-run hint for a rebuild:\n%s", a.Detail)
	}
}

// A network in the state it should be in still says nothing.
func TestCheckNetworkStateMismatch_silentWhenEveryNetworkIsFine(t *testing.T) {
	if a := checkNetworkStateMismatch([]docker.NetworkStatus{
		{Name: "overcast"},
		{Name: "overcast_control", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"},
	}); a != nil {
		t.Fatalf("advisory = %+v, want none", a)
	}
}
