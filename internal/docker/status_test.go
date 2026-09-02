package docker

// status_test.go covers the Tracker's network reporting — the half of #1564
// that makes the control plane's isolation answerable without reading the
// startup log: `/_overcast/health` renders whatever ends up here under
// `docker.networks`.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTracker_recordsTheResolvedNetworkIsolation(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{
		{Name: "overcast"},
		{Name: "overcast_control", Internal: true, Reason: "auto: native Linux Docker daemon"},
	})

	got := tr.Snapshot().Networks
	if len(got) != 2 {
		t.Fatalf("Networks = %+v, want two entries", got)
	}
	if got[1].Name != "overcast_control" || !got[1].Internal ||
		got[1].Reason != "auto: native Linux Docker daemon" {
		t.Errorf("control plane entry = %+v, want the decision and its reason", got[1])
	}
}

// Probe runs once per Docker daemon, and services are routinely spread over
// several socket settings that address the same one — so the same planes are
// reported more than once. They have to merge by name rather than accumulate,
// or /_overcast/health grows a duplicate row per service.
func TestTracker_mergesRepeatedNetworkReportsByName(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast_control", Internal: false, Reason: "first"}})
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast_control", Internal: true, Reason: "second"}})
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast"}})

	got := tr.Snapshot().Networks
	if len(got) != 2 {
		t.Fatalf("Networks = %+v, want two entries after a repeated report", got)
	}
	if got[0].Reason != "second" || !got[0].Internal {
		t.Errorf("Networks[0] = %+v, want the later report to have won", got[0])
	}
}

// Snapshot has to deep-copy: the health handler serialises what it returns
// while Probe may still be recording on another goroutine.
func TestTracker_snapshotDoesNotAliasTheNetworkSlice(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast_control", Internal: true}})

	snap := tr.Snapshot()
	snap.Networks[0].Internal = false

	if !tr.Snapshot().Networks[0].Internal {
		t.Error("mutating a snapshot changed the tracker's own state")
	}
}

// The wire names are what /_overcast/health publishes and the web UI's
// generated types are built from, so they are pinned here rather than left to
// whatever the field names happen to be.
func TestNetworkStatus_jsonShape(t *testing.T) {
	body, err := json.Marshal(Status{
		Available: true,
		Networks: []NetworkStatus{{
			Name: "overcast_control", Internal: true,
			Reason: "OVERCAST_CONTROL_PLANE_INTERNAL=true",
			Drift:  "network predates this configuration: internal=false",
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"networks":[{`, `"name":"overcast_control"`, `"internal":true`,
		`"reason":"OVERCAST_CONTROL_PLANE_INTERNAL=true"`, `"drift":`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("health JSON %s does not contain %s", body, want)
		}
	}

	// Absent rather than null when there is nothing to say: a status with no
	// networks is what a metadata-only run reports, and an empty key there
	// would read as "no planes" rather than "not probed".
	body, err = json.Marshal(Status{Available: false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "networks") {
		t.Errorf("health JSON %s carries an empty networks key", body)
	}
}
